package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/views"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/record"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/view"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/turn"
	"github.com/wloophq/wloop/internal/workspace"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// bookOf is the pull request's turns as the daemon keeps them.
func (te *testEnv) bookOf(t *testing.T, ref github.Ref) dturn.Book {
	t.Helper()
	books, err := te.repo.Books(context.Background(), pr.Ref(ref).Unit())
	if err != nil {
		t.Fatal(err)
	}
	return books[pr.Ref(ref).Unit()]
}

func warmReviewWorkspace() *fake.Manager {
	return &fake.Manager{
		RepoList:      []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}},
		WorkspaceList: []wsdomain.Workspace{{ID: "ws-rev", RepoID: "r1", Path: "/wt/rev", DisplayName: "REVIEW feat/x", HostID: "local", LinkedPR: 7}},
		Sessions:      map[string]string{"ws-rev": "term-rev"},
		LastOutput:    map[string]time.Time{"ws-rev": testNow.Add(-time.Minute)},
	}
}

func TestRoundsSkipSessionsWithoutMCP(t *testing.T) {
	t.Run("a live session without the tool gets no prompt", func(t *testing.T) {
		m := warmReviewWorkspace()
		te := newTestEnv(t, m)
		te.gh.basics = openBasics
		te.reg.withoutMCP("term-rev")
		if code := te.run("review", r7.URL()); code != 0 {
			t.Fatalf("exit %d: %s", code, te.err)
		}
		if len(m.Prompts) != 1 || m.Prompts[0].Handle != "term-ws-rev" {
			t.Fatalf("prompts %+v", m.Prompts)
		}
		if !strings.Contains(te.out.String(), verbNoMCP) {
			t.Fatalf("out: %s", te.out)
		}
	})
	t.Run("a new session that never initializes fails the round", func(t *testing.T) {
		defer func(d time.Duration) { mcpWait = d }(mcpWait)
		mcpWait = 50 * time.Millisecond
		m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}}
		te := newTestEnv(t, m)
		te.gh.basics = openBasics
		te.git.branch["/worktrees/REVIEW feat/x"] = "feat/x"
		te.reg.withoutMCP("term-ws-1")
		started := time.Now()
		if code := te.run("review", r7.URL()); code != 1 {
			t.Fatalf("exit %d", code)
		}
		if waited := time.Since(started); waited < mcpWait {
			t.Fatalf("gave up after %v", waited)
		}
		want := errNoMCP{te.agentHome(te.cfg)}.Error()
		if !strings.Contains(te.err.String(), want) || !strings.Contains(want, "check `"+brand.Command+" doctor` for "+te.Home+"/.claude") {
			t.Fatalf("err: %s", te.err)
		}
		if got := strings.Join(m.Writes, "\n"); got != "create REVIEW feat/x\nsession ws-1\nclose term-ws-1\nremove --force ws-1" || len(m.Prompts) != 0 {
			t.Fatalf("writes:\n%s\nprompts %+v", got, m.Prompts)
		}
		if b := te.bookOf(t, r7); len(b.Turns) != 0 {
			t.Fatalf("a round that typed nothing kept %+v", b.Turns)
		}
	})
}

func TestASessionThatCannotTakeTheRoundIsAbandoned(t *testing.T) {
	trustedBasics := strings.Replace(openBasics, `"headRefOid":"c2",`, `"headRefOid":"c2","authorAssociation":"MEMBER",`, 1)
	t.Run("one that never initializes, beside the live one", func(t *testing.T) {
		defer func(d time.Duration) { mcpWait = d }(mcpWait)
		mcpWait = 50 * time.Millisecond
		m := warmReviewWorkspace()
		te := newTestEnv(t, m)
		te.gh.basics = openBasics
		te.reg.withoutMCP("term-rev")
		te.reg.withoutMCP("term-ws-rev")
		if code := te.run("review", r7.URL()); code != 1 {
			t.Fatalf("exit %d: %s", code, te.err)
		}
		abandoned := te.sessionOf(t, "fake", "term-ws-rev")
		if !strings.Contains(te.err.String(), "closed its session "+abandoned) {
			t.Fatalf("stderr: %s", te.err)
		}
		if got := strings.Join(m.Writes, "\n"); !strings.Contains(got, "close term-ws-rev") || len(m.Prompts) != 0 {
			t.Fatalf("writes:\n%s\nprompts %+v", got, m.Prompts)
		}
		if s, _, _ := te.reg.Registry.Session(context.Background(), abandoned); s.ClosedAt.IsZero() {
			t.Fatalf("the abandoned session is not marked: %+v", s)
		}
		if ok, _ := te.Turns.Capable(context.Background(), abandoned); ok {
			t.Fatal("a later round could type into the abandoned session")
		}
	})
	t.Run("one of a trusted pull request blocked on the trust dialog", func(t *testing.T) {
		m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}, Blocked: workspace.ReasonTrust}
		te := newTestEnv(t, m)
		te.gh.basics = trustedBasics
		te.git.branch["/worktrees/REVIEW feat/x"] = "feat/x"
		if code := te.run("review", r7.URL()); code != 1 {
			t.Fatalf("exit %d", code)
		}
		if got := strings.Join(m.Writes, "\n"); got != "create REVIEW feat/x\nsession ws-1\nclose term-ws-1\nremove --force ws-1" {
			t.Fatalf("writes:\n%s", got)
		}
	})
	t.Run("one of an untrusted pull request blocked on a prompt stays for the user", func(t *testing.T) {
		m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}, Blocked: "the trust dialog"}
		te := newTestEnv(t, m)
		te.gh.basics = openBasics
		te.git.branch["/worktrees/REVIEW feat/x"] = "feat/x"
		if code := te.run("review", r7.URL()); code != 1 || !strings.Contains(te.err.String(), "waits on the trust dialog in its terminal term-ws-1") {
			t.Fatalf("exit %d: %s", code, te.err)
		}
		if got := strings.Join(m.Writes, "\n"); got != "create REVIEW feat/x\nsession ws-1" {
			t.Fatalf("writes:\n%s", got)
		}
	})
}

func TestTrusts(t *testing.T) {
	t.Parallel()
	cfg := config.Config{Plugins: config.Plugins{GitHub: config.GitHub{TrustedAuthors: []string{"bot[bot]"}}}}
	for _, c := range []struct {
		name string
		pr   github.PullRequest
		want bool
	}{
		{"member", github.PullRequest{Facts: pr.Facts{Ref: r7, Author: "ann", AuthorAssociation: "MEMBER"}}, true},
		{"outsider", github.PullRequest{Facts: pr.Facts{Ref: r7, Author: "eve", AuthorAssociation: "NONE"}}, false},
		{"trusted author", github.PullRequest{Facts: pr.Facts{Ref: r7, Author: "bot[bot]", AuthorAssociation: "NONE"}}, true},
		{"fork", github.PullRequest{Facts: pr.Facts{Ref: r7, Author: "ann", AuthorAssociation: "MEMBER", IsCrossRepository: true}}, false},
	} {
		if got := trusts(cfg, &c.pr); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
	if trusts(cfg, nil) {
		t.Error("a pull request not read is trusted")
	}
}

func TestRoundStartWritesTheBoardThroughTheViews(t *testing.T) {
	t.Parallel()
	m := warmReviewWorkspace()
	te := newTestEnv(t, m)
	te.gh.basics = openBasics
	ctx := context.Background()
	te.sink.Place(pr.Ref(r7).Unit(), "later")
	ev := port.Event{Unit: pr.Ref(r7).Unit(), Kind: views.KindViewOverride, Data: "later", At: testNow}
	if err := te.repo.SaveApplied(ctx, te.sink.Name(), view.Applied{Unit: pr.Ref(r7).Unit(), Role: view.ReviewRole, Status: view.Reviewed, Override: "later", At: testNow}, ev); err != nil {
		t.Fatal(err)
	}
	te.runViews(t)
	if code := te.run("review", r7.URL()); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	eventually(t, "the card in the working column", func() bool {
		column, _, _ := te.sink.Card(pr.Ref(r7).Unit())
		return column == te.sink.Column(view.Working)
	})
	eventually(t, "the override cleared", func() bool {
		applied, err := te.repo.Applied(ctx, te.sink.Name())
		a := applied[view.Key{Unit: pr.Ref(r7).Unit(), Role: view.ReviewRole}]
		return err == nil && a.Status == view.Working && a.Override == ""
	})
	for _, w := range m.Writes {
		if strings.HasPrefix(w, "status ") {
			t.Fatalf("the round wrote the board itself: %v", m.Writes)
		}
	}
}

func TestDeliverKeepsATurnTheAgentStartedFirst(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{}
	te := newTestEnv(t, m)
	ctx := context.Background()
	sent, err := te.sendTurn(ctx, te.cfg, turn.Request{Intent: turn.Intent(dintent.Review), Trigger: turn.ByHand, Unit: r7.Unit(), Payload: &turn.PR{Title: "Add x", Head: "c2"}})
	if err != nil {
		t.Fatal(err)
	}
	wrapped := &startingWS{Manager: m, start: func() error { _, err := te.Turns.Start(ctx, "term-1", sent.ID); return err }}
	te.WS = workspace.NewSet(wrapped)
	if _, err := te.deliver(ctx, r7.Unit(), &sent, proc{"term-1", wrapped, "term-1"}, false); err != nil {
		t.Fatal(err)
	}
	if b := te.bookOf(t, r7); len(b.Turns) != 1 || b.Turns[0].State != dturn.Started || b.Turns[0].Session != "term-1" || sent.Seq != b.Turns[0].Seq {
		t.Fatalf("turns %+v, seq %d", b.Turns, sent.Seq)
	}
}

// startingWS runs start, as an agent starting its turn would, when a prompt
// is typed.
type startingWS struct {
	*fake.Manager
	start func() error
}

func (w *startingWS) SendPrompt(ctx context.Context, handle, prompt string, queued bool) (bool, error) {
	ok, err := w.Manager.SendPrompt(ctx, handle, prompt, queued)
	if err != nil {
		return ok, err
	}
	return ok, w.start()
}

func TestAPromptThatCannotBeTypedLeavesNoQuestion(t *testing.T) {
	t.Parallel()
	m := warmReviewWorkspace()
	m.SendFails = errors.New("orca: terminal closed")
	te := newTestEnv(t, m)
	te.gh.basics = openBasics
	if code := te.run("review", r7.URL()); code != 1 || !strings.Contains(te.err.String(), "terminal closed") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	b := te.bookOf(t, r7)
	if len(b.Turns) != 1 || b.Turns[0].State != dturn.Withdrawn {
		t.Fatalf("turns %+v", b.Turns)
	}
	te.Now = func() time.Time { return testNow.Add(time.Hour) }
	if rep, err := te.Turns.Reconcile(context.Background(), false); err != nil || len(rep.Unrecorded) != 0 {
		t.Fatalf("reconcile %+v %v", rep, err)
	}
}

func TestAFailedResumeLeavesTheQuestionOpen(t *testing.T) {
	t.Parallel()
	m := warmReviewWorkspace()
	m.SendFails = errors.New("orca: terminal closed")
	te := newTestEnv(t, m)
	te.gh.basics = openBasics
	ctx := context.Background()
	ref := pr.Ref(r7)
	b := dturn.Book{Unit: ref.Unit(), Turns: []dturn.Turn{{Intent: dturn.Intent(dintent.Review), ID: "q", Trigger: dturn.Automated, Head: "c2", Session: "term-gone", State: dturn.Asked, SentAt: testNow}}}
	b.Records = []dturn.Record{{Session: "term-gone", Hash: "h", At: testNow, Record: record.Record{State: record.Asked, Summary: "publish the approval?"}}}
	saved, err := te.repo.Save(ctx, b, port.Event{Unit: ref.Unit(), Kind: turns.KindTurnSent, Key: "q", At: testNow})
	if err != nil {
		t.Fatal(err)
	}
	err = cliLauncher{te.Env}.Launch(ctx, turns.Launch{Unit: ref.Unit(), Intent: dturn.Intent(dintent.Review), ResumeOf: saved.Turns[0].Seq, Answer: "publish"})
	if err == nil || !strings.Contains(err.Error(), "terminal closed") {
		t.Fatalf("err = %v", err)
	}
	got := te.bookOf(t, r7)
	if len(got.Turns) != 2 || got.Turns[0].State != dturn.Asked || got.Turns[1].State != dturn.Withdrawn {
		t.Fatalf("turns %+v", got.Turns)
	}
	if q, ok := got.Question([]string{"review", "address", "probe"}); !ok || q.Seq != saved.Turns[0].Seq || !got.OpenQuestion(dturn.Intent(dintent.Review)) {
		t.Fatalf("question %+v %v", q, ok)
	}
}

// failingDelivery fails the save that names the session a turn was typed
// into.
type failingDelivery struct{ turns.Store }

func (f failingDelivery) Save(ctx context.Context, b dturn.Book, evs ...port.Event) (dturn.Book, error) {
	for _, ev := range evs {
		if ev.Kind == turns.KindTurnDelivered {
			return dturn.Book{}, errors.New("disk full")
		}
	}
	return f.Store.Save(ctx, b, evs...)
}

func TestATypedRoundSurvivesAFailureToNameItsSession(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}}
	te := newTestEnv(t, m)
	te.gh.basics = openBasics
	te.git.branch["/worktrees/REVIEW feat/x"] = "feat/x"
	te.Turns.Store = failingDelivery{te.repo}
	if code := te.run("review", r7.URL()); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if !strings.Contains(te.err.String(), "warning: acme/api#7: naming the session of round") || !strings.Contains(te.err.String(), "disk full") {
		t.Fatalf("err: %s", te.err)
	}
	if got := strings.Join(m.Writes, "\n"); got != "create REVIEW feat/x\nsession ws-1\nsend term-ws-1\nlink ws-1 7" {
		t.Fatalf("writes:\n%s", got)
	}
	if b := te.bookOf(t, r7); len(b.Turns) != 1 || b.Turns[0].State != dturn.Sent || b.Turns[0].SentTo != "" {
		t.Fatalf("turns %+v", b.Turns)
	}
}
