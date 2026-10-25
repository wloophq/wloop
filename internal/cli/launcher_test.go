package cli

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/record"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

var r21 = github.Ref{Owner: "acme", Repo: "api", Number: 21}

// stoppedAddress stores an address turn of r21 whose session stopped
// without a record, and returns its seq.
func stoppedAddress(t *testing.T, te *testEnv) int64 {
	t.Helper()
	return stoppedAddressWith(t, te, nil)
}

func stoppedAddressWith(t *testing.T, te *testEnv, inputs map[string]any) int64 {
	t.Helper()
	ctx := context.Background()
	ref := pr.Ref(r21)
	trigger := dturn.Automated
	if inputs != nil {
		trigger = dturn.ByHand
	}
	b := dturn.Book{Unit: ref.Unit(), Turns: []dturn.Turn{{Intent: dturn.Intent(dintent.Address), ID: "r0", Trigger: trigger, Head: "c20",
		Guidance: "keep the API", State: dturn.Unrecorded, SentAt: testNow, EndedAt: testNow, Inputs: inputs}}}
	b.Records = []dturn.Record{{Session: "term-old", Head: "c20", Hash: "h0", At: testNow, Record: record.Unrecorded("was fixing the flaky test")}}
	saved, err := te.repo.Save(ctx, b, port.Event{Unit: ref.Unit(), Kind: turns.KindTurnSent, Key: "r0", At: testNow})
	if err != nil {
		t.Fatal(err)
	}
	return saved.Turns[0].Seq
}

func myPRManager() *fake.Manager {
	return &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}}
}

func TestResumeRecreatesTheAuthorWorkspace(t *testing.T) {
	t.Parallel()
	m := myPRManager()
	te := newTestEnv(t, m)
	te.gh.basics = addressBasics(t, obj{"__typename": "User", "login": "me"})
	te.git.branch["/worktrees/feat/mine"] = "feat/mine"
	te.git.ancestor["/worktrees/feat/mine"] = true
	seq := stoppedAddress(t, te)

	err := cliLauncher{te.Env}.Launch(context.Background(), turns.Launch{Unit: pr.Ref(r21).Unit(), Intent: dturn.Intent(dintent.Address), ResumeOf: seq, Answer: "go on"})
	if err != nil {
		t.Fatalf("%v\n%s", err, te.err)
	}
	if got := strings.Join(m.Writes, "\n"); got != "create feat/mine\nsession ws-1\nlink ws-1 21\nsend term-ws-1" {
		t.Fatalf("writes:\n%s", strings.Join(m.Writes, "\n"))
	}
	if len(m.Prompts) != 1 || m.Prompts[0].Handle != "term-ws-1" {
		t.Fatalf("prompts %+v", m.Prompts)
	}
	text := turnText(t, te, m.Prompts[0].Text)
	for _, want := range []string{"## Where the last session stopped", "was fixing the flaky test", "go on", "keep the API"} {
		if !strings.Contains(text, want) {
			t.Fatalf("no %q in the turn:\n%s", want, text)
		}
	}
	b := te.bookOf(t, r21)
	if len(b.Turns) != 2 {
		t.Fatalf("turns %+v", b.Turns)
	}
	resume := b.Turns[1]
	if resume.ResumeOf != seq || resume.Trigger != dturn.Automated || resume.State != dturn.Sent || resume.SentTo != te.sessionOf(t, "fake", "term-ws-1") || resume.Head != "c21" {
		t.Fatalf("resume turn %+v", resume)
	}
}

func TestAResumeKeepsTheSelfReview(t *testing.T) {
	t.Parallel()
	m := myPRManager()
	te := newTestEnv(t, m)
	te.gh.basics = addressBasics(t, obj{"__typename": "User", "login": "me"})
	te.git.branch["/worktrees/feat/mine"] = "feat/mine"
	te.git.ancestor["/worktrees/feat/mine"] = true
	seq := stoppedAddressWith(t, te, map[string]any{"self_review": true})

	err := cliLauncher{te.Env}.Launch(context.Background(), turns.Launch{Unit: pr.Ref(r21).Unit(), Intent: dturn.Intent(dintent.Address), ResumeOf: seq, Answer: "go on"})
	if err != nil {
		t.Fatalf("%v\n%s", err, te.err)
	}
	if len(m.Prompts) != 1 {
		t.Fatalf("prompts %+v", m.Prompts)
	}
	text := turnText(t, te, m.Prompts[0].Text)
	for _, want := range []string{"#### Your own review of the change", "review again only what changed since", "## Review judgment"} {
		if !strings.Contains(text, want) {
			t.Fatalf("no %q in the turn:\n%s", want, text)
		}
	}
	if b := te.bookOf(t, r21); len(b.Turns) != 2 || b.Turns[1].Inputs["self_review"] != true {
		t.Fatalf("turns %+v", b.Turns)
	}
}

func TestResumeRefusesUnpushedCommits(t *testing.T) {
	t.Parallel()
	m := myPRManager()
	te := newTestEnv(t, m)
	te.gh.basics = addressBasics(t, obj{"__typename": "User", "login": "me"})
	te.git.tips["feat/mine"] = "local1"
	seq := stoppedAddress(t, te)

	err := cliLauncher{te.Env}.Launch(context.Background(), turns.Launch{Unit: pr.Ref(r21).Unit(), Intent: dturn.Intent(dintent.Address), ResumeOf: seq, Answer: "go on"})
	if err == nil || !strings.Contains(err.Error(), "local branch feat/mine has commits not on the PR") {
		t.Fatalf("err = %v", err)
	}
	if len(m.Prompts) != 0 || strings.Contains(strings.Join(m.Writes, "\n"), "create") {
		t.Fatalf("writes %v, prompts %+v", m.Writes, m.Prompts)
	}
	if b := te.bookOf(t, r21); len(b.Turns) != 1 {
		t.Fatalf("turns %+v", b.Turns)
	}
}

func TestResumeOfAReviewRunsInTheReviewWorkspace(t *testing.T) {
	t.Parallel()
	m := warmReviewWorkspace()
	te := newTestEnv(t, m)
	te.gh.basics = openBasics
	ctx := context.Background()
	ref := pr.Ref(r7)
	saved, err := te.repo.Save(ctx, dturn.Book{Unit: ref.Unit(), Turns: []dturn.Turn{{Intent: dturn.Intent(dintent.Review), ID: "r0", Trigger: dturn.ByHand, Head: "c1", State: dturn.Unrecorded, SentAt: testNow}}},
		port.Event{Unit: ref.Unit(), Kind: turns.KindTurnSent, Key: "r0", At: testNow})
	if err != nil {
		t.Fatal(err)
	}
	seq := saved.Turns[0].Seq
	if err := (cliLauncher{te.Env}).Launch(ctx, turns.Launch{Unit: ref.Unit(), Intent: dturn.Intent(dintent.Review), ResumeOf: seq, Answer: "publish"}); err != nil {
		t.Fatalf("%v\n%s", err, te.err)
	}
	if len(m.Prompts) != 1 || m.Prompts[0].Handle != "term-rev" {
		t.Fatalf("prompts %+v", m.Prompts)
	}
	if b := te.bookOf(t, r7); len(b.Turns) != 2 || b.Turns[1].ResumeOf != seq || b.Turns[1].SentTo != te.sessionOf(t, "fake", "term-rev") {
		t.Fatalf("turns %+v", b.Turns)
	}
}

// askedIntent runs a round of the intent the user asked for through the
// daemon's action, as envActions.Intent does.
func askedIntent(e *Env, cfg config.Config, ref github.Ref, name dintent.Name, guidance string, inputs map[string]any, home string) error {
	d, ok := e.loadedIntents().Registry.Get(name)
	if !ok {
		return fmt.Errorf("no intent %s", name)
	}
	return e.askedRound(context.Background(), d, cfg, ref, guidance, inputs, home)
}

func nudgePR(_ context.Context, e *Env, cfg config.Config, ref github.Ref, situation string, maintainers bool, guidance, home string) error {
	return askedIntent(e, cfg, ref, dintent.Nudge, guidance, map[string]any{"situation": situation, "waits_on_maintainers": maintainers}, home)
}
