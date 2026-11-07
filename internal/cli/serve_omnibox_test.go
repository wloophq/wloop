package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/core/lock"
	"github.com/wloophq/wloop/internal/core/omnibox"
	"github.com/wloophq/wloop/internal/core/turns"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

type costAsker struct {
	cost float64
	asks []turns.Ask
}

func (a *costAsker) Ask(_ context.Context, q turns.Ask) (turns.Answer, error) {
	a.asks = append(a.asks, q)
	return turns.Answer{JSON: []byte(`{"answer":{"text":"none","tasks":[]}}`), CostUSD: &a.cost}, nil
}

func TestTheOmniboxCallIsBookedOnTheHeadlessCaps(t *testing.T) {
	t.Parallel()
	te, db := servedDossiers(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	d := te.serveDeps(ctx, db, te.cfg, t.Logf, &lock.Mutex{}, nil)
	a, ok := d.OmniboxAsk.(*omnibox.Asker)
	if !ok || a.Budget != te.callBudget || a.Budget != te.dossierCalls.Budget || a.Wrapper == nil {
		t.Fatalf("the omnibox asker is not wired on the calls' budget and the profile's wrapper: %+v", d.OmniboxAsk)
	}
	agent := &costAsker{cost: 0.4}
	a.Agent = agent
	if _, err := a.Ask(ctx, omnibox.AskRequest{Text: "what waits on me?"}, omnibox.Snapshot{}); err != nil {
		t.Fatal(err)
	}
	if len(agent.asks) != 1 || !strings.HasPrefix(agent.asks[0].Prompt, "This is the omnibox of ") {
		t.Fatalf("asks %+v", agent.asks)
	}
	svc := serveDepsAgain(t, te, db)
	if spent, err := svc.Spent(ctx, testNow); err != nil || spent != 0.4 {
		t.Fatalf("the headless cap reads %v spent: %v", spent, err)
	}
}

func TestTheOmniboxWrapperResolvesThroughTheProfile(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	userProfile(t, te, map[string]string{omnibox.WrapperFile: `{{define "before"}}MINE in {{.Dir}}
{{end}}{{define "follow"}}ASKED {{.Question}} GOT {{.Answer}}
{{end}}{{define "after"}}MY RULES
{{end}}`})
	w, err := profilePrompt(te.Env, t.Logf, "omnibox", omnibox.WrapperFile, omnibox.ParseWrapper)()
	if err != nil {
		t.Fatal(err)
	}
	p, err := w.Prompt(omnibox.AskRequest{Text: "x", Follow: []omnibox.AskFollowUp{{Question: "which?", Answer: "both"}}}, "/snap")
	if err != nil || !strings.HasPrefix(p, "MINE in /snap") || !strings.Contains(p, "ASKED which? GOT both\n") ||
		!strings.Contains(p, "MY RULES\n"+omnibox.Schema()) {
		t.Fatalf("%v\n%s", err, p)
	}
}

func TestTheOmniboxAgentRunsApartFromTheSnapshots(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.cfg = withoutWindows(te.cfg)
	left := filepath.Join(te.StateDir, "omnibox", "snapshots", "ask-left")
	if err := os.MkdirAll(left, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), storeFile), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	d := te.serveDeps(ctx, db, te.cfg, t.Logf, &lock.Mutex{}, nil)
	a := d.OmniboxAsk.(*omnibox.Asker)
	agent, ok := a.Agent.(lazyAsker)
	if !ok || agent.dir != filepath.Join(te.StateDir, "omnibox", "run") || a.Dir != filepath.Join(te.StateDir, "omnibox", "snapshots") {
		t.Fatalf("the agent runs in %q and the snapshots go in %q", agent.dir, a.Dir)
	}
	if _, err := os.Stat(left); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a snapshot a dead call left survived the serve: %v", err)
	}
}

func TestTheServedOmniboxAskerUsesTheVocabulary(t *testing.T) {
	t.Skip("B10: wire Asker.Allowed to Vocabulary.Allowed and Snapshot.Vocabulary")
	te, db := servedDossiers(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a := te.serveDeps(ctx, db, te.cfg, t.Logf, &lock.Mutex{}, nil).OmniboxAsk.(*omnibox.Asker)
	if a.Allowed == nil {
		t.Fatal("the served asker has no vocabulary")
	}
	if why, _ := a.Allowed(omnibox.AskOp{Op: "action", Action: "review"}); why != "" {
		t.Fatalf("the bundled review verb is refused: %s", why)
	}
	if why, _ := a.Allowed(omnibox.AskOp{Op: "verb", Verb: "approve"}); why == "" {
		t.Fatal("approve, which resolves a gate, is allowed")
	}
}

func TestOmniboxReposNameEachRepositoryOnce(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{RepoList: []wsdomain.Repo{
		{ID: "r1", RemoteKey: "forge.example/acme/web"}, {ID: "r2", RemoteKey: "forge.example/acme/api"},
		{ID: "r3", RemoteKey: "forge.example/acme/api"}, {ID: "scratch"}}})
	got, err := te.Env.omniboxRepos(context.Background())
	if want := []string{"acme/api", "acme/web", "scratch"}; err != nil || !slices.Equal(got, want) {
		t.Fatalf("%v %v, want %v", got, err, want)
	}
}
