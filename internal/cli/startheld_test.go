package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/gates/gatestest"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/gate"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/workspace"
)

// heldBlock is the one start block recorded on ref.
func heldBlock(t *testing.T, rec *memRecorder, ref pr.Ref) (dinv.Event, inventory.StartBlock) {
	t.Helper()
	var blocked []dinv.Event
	for _, e := range blocksOf(rec, ref) {
		if e.Kind == dinv.KindRoundBlocked {
			blocked = append(blocked, e)
		}
	}
	if len(blocked) != 1 {
		t.Fatalf("round_blocked events %+v", blocksOf(rec, ref))
	}
	b, ok := inventory.StartBlockOf(port.Event{Unit: ref.Unit(), Kind: blocked[0].Kind, Key: blocked[0].Key})
	if !ok {
		t.Fatalf("the key of %+v names no start block", blocked[0])
	}
	return blocked[0], b
}

func TestAReviewByHandStoppedOnTheTrustDialogIsHeldAsTheSweepHoldsIt(t *testing.T) {
	t.Parallel()
	m := newRepo()
	m.Blocked = workspace.ReasonTrust
	te := newTestEnv(t, m)
	def, profile, _ := blindHomes(t, te)
	te.gh.basics = trustedBasics
	te.git.branch["/worktrees/REVIEW feat/x"] = "feat/x"
	rec := &memRecorder{}
	te.Rec = rec
	ctx := context.Background()

	err := (envActions{e: te.Env}).Intent(ctx, string(dintent.Review), r7.Unit(), daemon.Call{Guidance: "look at the migration", Out: io.Discard})
	var untrusted *turns.UntrustedError
	if err == nil || !strings.Contains(err.Error(), "the round waits on its start block") {
		t.Fatalf("Intent = %v", err)
	}
	if !errors.As(err, &untrusted) {
		t.Fatalf("Intent = %v, not an untrusted folder", err)
	}
	ev, b := heldBlock(t, rec, r7)
	if b.Question != turns.QuestionTrust || !slices.Equal(b.Homes, []string{def, profile}) || b.Round != string(dintent.Review) {
		t.Fatalf("block %+v", b)
	}
	if b.Asked == nil || b.Asked.Intent != string(dintent.Review) || b.Asked.Guidance != "look at the migration" {
		t.Fatalf("asked %+v", b.Asked)
	}
	if !strings.HasPrefix(ev.Data, "could not start: /worktrees/REVIEW feat/x is not trusted in the agent home of its terminal") {
		t.Fatalf("data %q", ev.Data)
	}
	if got := strings.Join(m.Writes, "\n"); !strings.HasPrefix(got, "create REVIEW feat/x\n") || !strings.HasSuffix(got, "remove --force ws-1") {
		t.Fatalf("writes:\n%s", got)
	}
	st := gatestest.NewStore(te.Now)
	te.gates = gates.New(gates.Deps{Store: st, Reader: st, Profile: profileGates{te.loadedIntents}, Clock: clockFunc(te.Now)})
	te.gateReader = st
	if err := te.syncTrustGates(ctx); err != nil {
		t.Fatal(err)
	}
	if open, _ := st.Where(ctx, gates.Query{Kind: gate.KindTrust}); len(open) != 1 || open[0].Subject != ev.Key {
		t.Fatalf("trust gates %+v, want one on the block", open)
	}

	m.Blocked = ""
	var out strings.Builder
	if err := (envActions{e: te.Env}).TrustAndRun(ctx, r7.Unit(), daemon.Call{Out: &out}); err != nil {
		t.Fatalf("TrustAndRun = %v\nout:\n%s\nstderr:\n%s", err, &out, te.err)
	}
	for _, f := range []string{filepath.Join(te.Home, ".claude.json"), filepath.Join(profile, ".claude.json")} {
		if raw, _ := os.ReadFile(f); !strings.Contains(string(raw), `"/worktrees/REVIEW feat/x"`) {
			t.Errorf("%s does not trust the worktree:\n%s", f, raw)
		}
	}
	if len(m.Prompts) != 1 || !strings.Contains(turnText(t, te, m.Prompts[0].Text), "look at the migration") {
		t.Fatalf("prompts %+v\nstderr:\n%s", m.Prompts, te.err)
	}
	if _, intent, _, _ := promptOf(t, m.Prompts[0].Text); intent != string(dintent.Review) {
		t.Fatalf("the round run is %q, not the review asked for", intent)
	}
}

func TestEveryRoundByHandStoppedOnAQuestionIsHeldByAStartBlock(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		intent  dintent.Name
		screen  string
		blocked string
		want    string
	}{
		{intent: dintent.Address, blocked: workspace.ReasonTrust, want: turns.QuestionTrust},
		{intent: dintent.Address, screen: importsScreen, want: "agent-external-imports"},
	} {
		t.Run(string(c.intent)+" on "+c.want, func(t *testing.T) {
			t.Parallel()
			m, te := reviewedAuthorScenario(t)
			m.Sessions = nil
			m.Blocked, m.Screen = c.blocked, c.screen
			te.cfg.AgentHome = t.TempDir()
			te.gh.basics = pr9Basics
			rec := &memRecorder{}
			te.Rec = rec
			err := (envActions{e: te.Env}).Intent(context.Background(), string(c.intent), pr9.Unit(), daemon.Call{Guidance: "be brief", Out: io.Discard})
			if err == nil {
				t.Fatal("a blocked start succeeded")
			}
			_, b := heldBlock(t, rec, pr9)
			if b.Question != c.want || b.Asked == nil || b.Asked.Intent != string(c.intent) || b.Asked.Guidance != "be brief" {
				t.Fatalf("block %+v, asked %+v", b, b.Asked)
			}
			if c.want == turns.QuestionTrust && !slices.Equal(b.Homes, []string{te.cfg.AgentHome}) {
				t.Fatalf("homes %v", b.Homes)
			}
		})
	}
}

func TestTrustHomesOfTheTerminalsOwnHomeAreTheDefaultAndTheManagersProfileHomes(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, newRepo())
	def, profile, project := blindHomes(t, te)
	if got := te.trustHomes(te.cfg, ""); !slices.Equal(got, []string{def, profile}) {
		t.Fatalf("homes %v", got)
	}
	if got := te.trustHomes(te.cfg, project); !slices.Equal(got, []string{project}) {
		t.Fatalf("homes of a named home %v", got)
	}
}
