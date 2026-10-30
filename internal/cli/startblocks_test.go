package cli

import (
	"context"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/turns"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
)

// answeredQuestions is an agent whose home or checkout answered every
// question.
type answeredQuestions struct{ turns.Agent }

func (answeredQuestions) BlockedOn(string) string { return "" }

func (answeredQuestions) Explain(context.Context, turns.AgentSession, string) error { return nil }

func (answeredQuestions) Unanswered(context.Context, turns.AgentSession, string) bool { return false }

// blockRig is an Env whose recorder holds a start block on #3, read with
// a clock the test moves.
type blockRig struct {
	sc  sweepCase
	rec *memRecorder
	now time.Time
	ref pr.Ref
}

func newBlockRig(t *testing.T, key string) *blockRig {
	t.Helper()
	sc := followupScenario(t)
	r := &blockRig{sc: sc, rec: &memRecorder{}, now: testNow, ref: pr.Ref{Owner: "acme", Repo: "api", Number: 3}}
	sc.env.Rec, sc.env.Now = r.rec, func() time.Time { return r.now }
	r.block(key, testNow.Add(-time.Hour))
	return r
}

func (r *blockRig) block(key string, at time.Time) {
	r.rec.events = append(r.rec.events, dinv.Event{PR: r.ref, Kind: dinv.KindRoundBlocked, Key: key, At: at, Data: "folder not trusted"})
}

func (r *blockRig) unblocks() []dinv.Event {
	var out []dinv.Event
	for _, e := range r.rec.events {
		if e.Kind == dinv.KindRoundUnblocked {
			out = append(out, e)
		}
	}
	return out
}

const (
	trustBlockKey = "2026-10-21T14:00:00Z\ntrust\n/w/app\n/w/app/sub\n\n\nfirst\n\n"
	mcpBlockKey   = "2026-10-21T14:00:00Z\nagent-project-mcp\n/w/app\n\n\n\nfirst\n\n"
)

func TestTheHostLiftsAStartBlockOnceADayPassesBlind(t *testing.T) {
	t.Parallel()
	r := newBlockRig(t, trustBlockKey)
	r.sc.env.Agents = nil
	b := &startBlocks{e: r.sc.env}
	if err := b.Refresh(context.Background()); err != nil || len(r.unblocks()) != 0 {
		t.Fatalf("without agents: unblocked %v, %v", r.unblocks(), err)
	}
	r.sc.env.Agents = map[string]turns.Agent{}
	if err := b.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !b.Due().Equal(testNow.Add(23*time.Hour)) || len(r.unblocks()) != 0 {
		t.Fatalf("due %v, unblocked %v; want the blind block's day", b.Due(), r.unblocks())
	}
	r.now = testNow.Add(24 * time.Hour)
	if err := b.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := r.unblocks(); len(got) != 1 || got[0].PR != r.ref {
		t.Fatalf("a day later unblocked %v", got)
	}

	r.now = testNow.Add(30 * time.Hour)
	r.block(trustBlockKey, testNow.Add(29*time.Hour))
	if err := b.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !b.Due().Equal(testNow.Add(53 * time.Hour)) {
		t.Fatalf("blocked again: due %v", b.Due())
	}
}

func TestTheHostLiftsAStartBlockTheCheckoutAnswers(t *testing.T) {
	t.Parallel()
	r := newBlockRig(t, mcpBlockKey)
	cfg, err := r.sc.env.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	r.sc.env.Agents = map[string]turns.Agent{cfg.Agent: answeredQuestions{}}
	if err := (&startBlocks{e: r.sc.env}).Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := r.unblocks(); len(got) != 1 || got[0].Data != "the agent's question is answered" {
		t.Fatalf("unblocked %v", got)
	}
}
