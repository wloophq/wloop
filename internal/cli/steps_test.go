package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	ghsource "github.com/wloophq/wloop/internal/adapter/source/github"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/automation/automationtest"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	"github.com/wloophq/wloop/internal/core/workspaces"
	wt "github.com/wloophq/wloop/internal/core/workspaces/workspacestest"
	"github.com/wloophq/wloop/internal/domain/round"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func asRefused(err error, r *automation.Refused) bool { return errors.As(err, r) }

func wfStep(use string, args ...string) workflow.Step {
	st := workflow.Step{Use: use}
	for i := 0; i+1 < len(args); i += 2 {
		if st.Args == nil {
			st.Args = map[string]any{}
		}
		st.Args[args[i]] = args[i+1]
	}
	return st
}

func TestEveryVocabularyStepHasAnAdapter(t *testing.T) {
	t.Parallel()
	reg := stepRegistry()
	var vocabulary []string
	for _, d := range builtin.Steps {
		vocabulary = append(vocabulary, d.Name)
		e, ok := reg[d.Name]
		if !ok || e.check == nil || e.run == nil {
			t.Errorf("%s has no adapter", d.Name)
		}
	}
	for name := range reg {
		if !slices.Contains(vocabulary, name) {
			t.Errorf("%s is not in the vocabulary", name)
		}
	}
}

func TestStepsContract(t *testing.T) {
	t.Parallel()
	automationtest.RunSteps(t, func(t *testing.T) automation.Steps {
		te := newTestEnv(t, &fake.Manager{})
		return newStepSet(te.Env, stepDeps{Units: automationtest.NewUnits(), Rounds: te.Rounds})
	})
}

func TestASessionCloseRefusedAtRunIsAGuardRefusal(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.registerSession(t, dturn.Session{ID: "term-1", WorkspaceID: "ws1", StartedAt: testNow})
	s := newStepSet(te.Env, stepDeps{Units: automationtest.NewUnits(), Rounds: te.Rounds})
	u := unit.Unit{Provider: unit.Session, Key: "term-1"}
	refusal, err := s.Check(context.Background(), wfStep("session.close"), u, workflow.Why{}, automation.CheckOpts{Observe: false})
	if err != nil || refusal == "" {
		t.Fatalf("check %q %v", refusal, err)
	}
	_, err = s.Run(context.Background(), wfStep("session.close"), u, workflow.Why{Workflow: "session-done"}, automation.RunOpts{})
	var refused automation.Refused
	if !asRefused(err, &refused) || refused.Reason != refusal {
		t.Fatalf("run: %v, check refused %q", err, refusal)
	}
}

func TestASessionCloseOfASessionGoneOrClosedIsMoot(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.registerSession(t, dturn.Session{ID: "term-1", WorkspaceID: "ws1", StartedAt: testNow, ClosedAt: testNow})
	s := newStepSet(te.Env, stepDeps{Units: automationtest.NewUnits(), Rounds: te.Rounds})
	for _, key := range []string{"term-1", "term-absent"} {
		u := unit.Unit{Provider: unit.Session, Key: key}
		var moot automation.Moot
		if _, err := s.Check(context.Background(), wfStep("session.close"), u, workflow.Why{}, automation.CheckOpts{}); !errors.As(err, &moot) || moot.Reason == "" {
			t.Fatalf("%s: check %v, want moot", key, err)
		}
		_, err := s.Run(context.Background(), wfStep("session.close"), u, workflow.Why{Workflow: "session-done"}, automation.RunOpts{})
		if !errors.As(err, &moot) {
			t.Fatalf("%s: run %v, want moot", key, err)
		}
	}
}

func TestTheOrphanCloseStepEndsOnlyADurableOrphanAndIsMootOnceTheSessionIsGone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	te := newTestEnv(t, &fake.Manager{})
	te.registerSession(t, dturn.Session{ID: "term-1", WorkspaceID: "ws1", Managed: true, StartedAt: testNow.Add(-4 * time.Hour), PromptSeenAt: testNow.Add(-3 * time.Hour)})
	sess := te.Turns.Sessions.(*turnstest.Sessions)
	sess.Put("ws1", turns.Terminal{Session: "term-1"})
	sess.Orphan("term-1")
	s := newStepSet(te.Env, stepDeps{Units: automationtest.NewUnits(), Rounds: te.Rounds})
	u := unit.Unit{Provider: unit.Session, Key: "term-1"}
	step := wfStep("session.close_orphan")
	if why, err := s.Check(ctx, step, u, workflow.Why{}, automation.CheckOpts{}); err != nil || why != turns.RefusalOrphanUnproven {
		t.Fatalf("before the census proves it: %q %v", why, err)
	}
	for i, epoch := range []string{"e1", "e1", "e2", "e2"} {
		sess.SetReading(epoch, testNow.Add(-10*time.Minute+time.Duration(i)*te.Turns.FastJob))
		if err := te.Turns.Census(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if why, err := s.Check(ctx, step, u, workflow.Why{}, automation.CheckOpts{}); err != nil || why != "" {
		t.Fatalf("a durable orphan: %q %v", why, err)
	}
	if _, err := s.Run(ctx, step, u, workflow.Why{Workflow: "session-orphan-close"}, automation.RunOpts{}); err != nil {
		t.Fatal(err)
	}
	if closed := sess.Closed(); len(closed) != 1 || closed[0] != "term-1" {
		t.Fatalf("closed %v", closed)
	}
	var moot automation.Moot
	if _, err := s.Check(ctx, step, unit.Unit{Provider: unit.Session, Key: "term-absent"}, workflow.Why{}, automation.CheckOpts{}); !errors.As(err, &moot) {
		t.Fatalf("an absent session: %v, want moot", err)
	}
}

func TestACloseOfADormantSessionWhoseOrphanLingersIsHeldNotBlocked(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	te := newTestEnv(t, &fake.Manager{})
	te.registerSession(t, dturn.Session{ID: "term-1", WorkspaceID: "ws1", Managed: true, StartedAt: testNow.Add(-4 * time.Hour), PromptSeenAt: testNow.Add(-3 * time.Hour)})
	sess := te.Turns.Sessions.(*turnstest.Sessions)
	sess.Put("ws1", turns.Terminal{Session: "term-1"})
	sess.Orphan("term-1")
	for i := range 2 {
		sess.SetReading("", testNow.Add(-10*time.Minute+time.Duration(i)*te.Turns.FastJob))
		if err := te.Turns.Census(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := te.Turns.MarkDormant(ctx, "term-1"); err != nil {
		t.Fatal(err)
	}
	if err := te.Turns.Census(ctx); err != nil {
		t.Fatal(err)
	}
	s := newStepSet(te.Env, stepDeps{Units: automationtest.NewUnits(), Rounds: te.Rounds})
	_, err := s.Check(ctx, wfStep("session.close"), unit.Unit{Provider: unit.Session, Key: "term-1"}, workflow.Why{}, automation.CheckOpts{})
	var held automation.Held
	if !errors.As(err, &held) || held.Reason != turns.RefusalOrphanLingers {
		t.Fatalf("check %v, want the close held while the orphan lingers", err)
	}
}

func TestARemovalIsNotBlockedByADormantSessionWhoseOrphanLingers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	te := newTestEnv(t, &fake.Manager{})
	te.registerSession(t, dturn.Session{ID: "term-1", WorkspaceID: "ws1", Managed: true, StartedAt: testNow.Add(-4 * time.Hour), PromptSeenAt: testNow.Add(-3 * time.Hour)})
	sess := te.Turns.Sessions.(*turnstest.Sessions)
	sess.Put("ws1", turns.Terminal{Session: "term-1"})
	sess.Orphan("term-1")
	for i := range 2 {
		sess.SetReading("", testNow.Add(-10*time.Minute+time.Duration(i)*te.Turns.FastJob))
		if err := te.Turns.Census(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := te.Turns.MarkDormant(ctx, "term-1"); err != nil {
		t.Fatal(err)
	}
	if err := te.Turns.Census(ctx); err != nil {
		t.Fatal(err)
	}
	if why, _ := te.Turns.CloseRefusal(ctx, "term-1"); why != turns.RefusalOrphanLingers {
		t.Fatalf("the session's close is not held: %q", why)
	}
	g := turnsGuard{e: te.Env}
	if why, err := g.Refusal(ctx, "ws1"); err != nil || why != "" {
		t.Fatalf("removal refused %q %v", why, err)
	}
	if n, err := g.closeCount(ctx, "ws1", "removed"); err != nil || n != 1 {
		t.Fatalf("closed %d %v, want the dormant session's record", n, err)
	}
	if got, _, _ := te.repo.Session(ctx, "term-1"); got.State() != dturn.SessionClosed || len(sess.Closed()) != 0 {
		t.Fatalf("session %+v, processes ended %v", got, sess.Closed())
	}
}

func TestTheCatalogWordsTheCloseRefusalsAPlanItemShows(t *testing.T) {
	t.Parallel()
	l := newTestEnv(t, &fake.Manager{}).loadedIntents()
	for _, key := range []string{turns.RefusalNoAgent, turns.RefusalTerminalClosed, turns.RefusalNoWorkspace, turns.RefusalOrphanUnproven, turns.RefusalOrphanMoved, turns.RefusalOrphanLingers, turns.RefusalCloseRaced} {
		if _, ok := l.Strings[key]; !ok {
			t.Errorf("strings.toml does not word %q", key)
		}
		if !slices.Contains(api.PlanReasons, key) {
			t.Errorf("api.PlanReasons lacks %q, so no client reads it through its catalog", key)
		}
	}
}

func TestASessionCloseOfATerminalOpenWithNoAgentSaysWhatToDo(t *testing.T) {
	t.Parallel()
	te, _ := noAgentSession(t, true)
	s := newStepSet(te.Env, stepDeps{Units: automationtest.NewUnits(), Rounds: te.Rounds})
	refusal, err := s.Check(context.Background(), wfStep("session.close"), unit.Unit{Provider: unit.Session, Key: "term-1"}, workflow.Why{}, automation.CheckOpts{})
	if err != nil || refusal != turns.RefusalNoAgent {
		t.Fatalf("check %q %v", refusal, err)
	}
}

// removalRig is a workspace step over the core's fakes, its round state bound.
type removalRig struct {
	s        *stepSet
	world    wt.World
	mgr      *wt.Manager
	fetches  *wt.FetchLog
	sessions *wt.Sessions
	u        unit.Unit
	units    *automationtest.Units
	te       *testEnv
}

func newRemovalRig(t *testing.T, c wt.Checkout) *removalRig {
	t.Helper()
	te := newTestEnv(t, &fake.Manager{})
	world := wt.FakeWorld(t, c)
	w := wsdomain.Workspace{ID: "ws1", RepoID: "r1", Path: world.Dir, Branch: wt.Branch, HostID: wsdomain.LocalHost, LinkedPR: 7}
	mgr := &wt.Manager{Workspaces: []wsdomain.Workspace{w, {ID: "main", RepoID: "r1", Path: world.Main, IsMain: true, HostID: wsdomain.LocalHost}},
		RepoList: []wsdomain.Repo{{ID: "r1", Path: world.Main}}}
	r := &removalRig{world: world, mgr: mgr, fetches: &wt.FetchLog{}, sessions: &wt.Sessions{}, te: te,
		u: wsdomain.UnitOf(w.Path), units: automationtest.NewUnits()}
	r.units.Put(r.u, workflow.Facts{"workspace.pr": "acme/api#7", "workspace.pr_state": "OPEN", "workspace.repo": "acme/api"})
	r.s = newStepSet(te.Env, stepDeps{Units: r.units, Rounds: te.Rounds, Removal: removalPorts{
		Manager: mgr, Remote: world.Remote, Tree: world.Tree, Files: world.Files, Fetched: r.fetches, Guard: r.sessions,
		Disposable: func() []string { return wt.Disposable },
	}})
	return r
}

func (r *removalRig) check(t *testing.T, observe bool) string {
	t.Helper()
	refusal, err := r.s.Check(context.Background(), wfStep("workspace.remove"), r.u, workflow.Why{}, automation.CheckOpts{Observe: observe})
	if err != nil {
		t.Fatal(err)
	}
	return refusal
}

func TestWorkspaceRemoveSkipsTheFetchDayInObserve(t *testing.T) {
	t.Parallel()
	r := newRemovalRig(t, wt.Checkout{})
	if got := r.check(t, false); got != workspaces.NotFetchedToday {
		t.Fatalf("apply: %q", got)
	}
	if got := r.check(t, true); got != "" {
		t.Fatalf("observe: %q", got)
	}
	if len(r.world.Remote.(*wt.Git).Fetches()) != 0 {
		t.Fatal("check fetched")
	}
}

func TestWorkspaceRemoveNoteFetchesTheItemsPullRequest(t *testing.T) {
	t.Parallel()
	r := newRemovalRig(t, wt.Checkout{})
	if err := r.s.Note(context.Background(), wfStep("workspace.remove"), r.u, workflow.Why{}, workspaces.NotFetchedToday); err != nil {
		t.Fatal(err)
	}
	if got := r.world.Remote.(*wt.Git).Fetches(); len(got) != 1 || r.fetches.Marks() != 1 {
		t.Fatalf("fetches %v, marks %d", got, r.fetches.Marks())
	}
	if got := r.check(t, false); got != "" {
		t.Fatalf("after the fetch: %q", got)
	}
	if err := r.s.Note(context.Background(), wfStep("workspace.remove"), r.u, workflow.Why{}, "1 modified tracked file in /w/ws"); err != nil {
		t.Fatal(err)
	}
	if r.fetches.Marks() != 1 {
		t.Fatal("another refusal fetched")
	}
}

func TestWorkspaceRemoveAppendsTheBackupCommand(t *testing.T) {
	t.Parallel()
	r := newRemovalRig(t, wt.Checkout{Unpushed: 2})
	if err := r.fetches.Mark(context.Background(), "ws1", testNow); err != nil {
		t.Fatal(err)
	}
	if got := r.check(t, false); !strings.Contains(got, "2 commits not pushed") || !strings.Contains(got, "; back them up with: git push origin HEAD:refs/heads/") {
		t.Fatalf("refusal %q", got)
	}
}

func TestWorkspaceRemoveCheckReusesItsLookWhileTheFactsHold(t *testing.T) {
	t.Parallel()
	r := newRemovalRig(t, wt.Checkout{})
	now := testNow
	r.te.Env.Now = func() time.Time { return now }
	if err := r.fetches.Mark(context.Background(), "ws1", testNow); err != nil {
		t.Fatal(err)
	}
	if got := r.check(t, false); got != "" {
		t.Fatalf("first look: %q", got)
	}
	dir := r.world.Tree.(*wt.Git).Dirs[r.world.Dir]
	dir.Untracked = append(dir.Untracked, "threads.txt")
	r.world.Files.(*wt.Files).Put(r.world.Dir+"/threads.txt", "notes")
	if got := r.check(t, false); got != "" {
		t.Fatalf("the facts did not move, yet it looked again: %q", got)
	}
	r.sessions.Refuse = map[string]string{"ws1": "its agent is working"}
	if got := r.check(t, false); got != "its agent is working" {
		t.Fatalf("the sessions are always asked: %q", got)
	}
	r.sessions.Refuse = nil
	r.units.Put(r.u, workflow.Facts{"workspace.pr": "acme/api#7", "workspace.pr_state": "OPEN", "workspace.repo": "acme/api", "workspace.untracked": 1})
	if got := r.check(t, false); !strings.Contains(got, "threads.txt") {
		t.Fatalf("moved facts: %q", got)
	}
	dir.Untracked = nil
	if got := r.check(t, false); !strings.Contains(got, "threads.txt") {
		t.Fatalf("a refusal is reused too: %q", got)
	}
	now = now.Add(2 * time.Hour)
	if err := r.fetches.Mark(context.Background(), "ws1", now); err != nil {
		t.Fatal(err)
	}
	if got := r.check(t, false); got != "" {
		t.Fatalf("a look older than sweep.slow: %q", got)
	}
}

// countedSessions is a session guard that says how many sessions it closed.
type countedSessions struct {
	*wt.Sessions
	n int
}

func (c countedSessions) closeCount(ctx context.Context, id, reason string) (int, error) {
	return c.n, c.Sessions.CloseAll(ctx, id, reason)
}

func TestARefusedLastLookAfterClosingSessionsIsRecorded(t *testing.T) {
	t.Parallel()
	for closed, suffix := range map[int]bool{0: false, 2: true} {
		r := newRemovalRig(t, wt.Checkout{})
		r.s.d.Removal.Guard = countedSessions{Sessions: r.sessions, n: closed}
		reads := 0
		r.world.Remote.(*wt.Git).AfterUnpushed = func(dir string, d *wt.Dir) {
			if reads++; reads == 1 {
				d.Unpushed++
			}
		}
		st := r.s.bindForTest(t)
		_, err := r.s.Run(context.Background(), wfStep("workspace.remove"), r.u, workflow.Why{}, automation.RunOpts{})
		var refused automation.Refused
		if !asRefused(err, &refused) || !strings.Contains(refused.Reason, "changed while it was checked") || strings.Contains(refused.Reason, "its sessions were closed") != suffix {
			t.Fatalf("%d closed: run: %v", closed, err)
		}
		if len(r.mgr.Removed()) != 0 || (st.Mark("acme/api#7", ghsource.MarkFirstSkipped) != "") {
			t.Fatalf("removed %v", r.mgr.Removed())
		}
	}
}

// noAgentSession registers term-1 in workspace ws1, with a process its
// manager lists when listed is set, and no agent in it.
func noAgentSession(t *testing.T, listed bool) (*testEnv, turnsGuard) {
	t.Helper()
	te := newTestEnv(t, &fake.Manager{})
	te.registerSession(t, dturn.Session{ID: "term-1", WorkspaceID: "ws1", Managed: true, StartedAt: testNow})
	if listed {
		te.Turns.Sessions.(*turnstest.Sessions).Put("ws1", turns.Terminal{Session: "term-1"})
	}
	return te, turnsGuard{e: te.Env}
}

func TestARemovalRefusesAListedTerminalWithNoAgent(t *testing.T) {
	t.Parallel()
	_, g := noAgentSession(t, true)
	if why, err := g.Refusal(context.Background(), "ws1"); err != nil || why != "session term-1: "+turns.RefusalNoAgent {
		t.Fatalf("refusal %q %v", why, err)
	}
}

func TestARemovalIgnoresASessionWhoseTerminalIsGone(t *testing.T) {
	t.Parallel()
	_, g := noAgentSession(t, false)
	if why, err := g.Refusal(context.Background(), "ws1"); err != nil || why != "" {
		t.Fatalf("refusal %q %v", why, err)
	}
	if n, err := g.closeCount(context.Background(), "ws1", "removed"); err != nil || n != 0 {
		t.Fatalf("closed %d %v", n, err)
	}
}

func TestARemovalRefusesAnAgentTheLoopNeverRegistered(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.reg.withoutMCP("term-9")
	terms := te.Turns.Sessions.(*turnstest.Sessions)
	terms.Put("ws1", turns.Terminal{Session: "term-sh"})
	g := turnsGuard{e: te.Env}
	if why, err := g.Refusal(context.Background(), "ws1"); err != nil || why != "" {
		t.Fatalf("a shell with no agent: refusal %q %v", why, err)
	}
	terms.Put("ws1", turns.Terminal{Session: "term-9", Agent: "claude"})
	if why, err := g.Refusal(context.Background(), "ws1"); err != nil || why != "session term-9: no session term-9" {
		t.Fatalf("refusal %q %v", why, err)
	}
}

func TestARemovalReadsTheAgentTerminalsFromTheSnapshot(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.reg.withoutMCP("term-9")
	snap := &wsdomain.Snapshot{TerminalScope: wsdomain.Scope{Complete: true}, Terminals: []wsdomain.Terminal{
		{Handle: "term-gone", WorkspaceID: "ws1", Agent: "claude"},
		{Handle: "term-sh", WorkspaceID: "ws1", Usable: true},
		{Handle: "term-other", WorkspaceID: "ws2", Agent: "claude", Usable: true},
	}}
	g := turnsGuard{e: te.Env, snap: staticSnapshot{snap}}
	if why, err := g.Refusal(context.Background(), "ws1"); err != nil || why != "" {
		t.Fatalf("an unusable terminal or a shell: refusal %q %v", why, err)
	}
	snap.Terminals = append(snap.Terminals, wsdomain.Terminal{Handle: "term-9", WorkspaceID: "ws1", Agent: "claude", Usable: true})
	if why, err := g.Refusal(context.Background(), "ws1"); err != nil || why != "session term-9: no session term-9" {
		t.Fatalf("refusal %q %v", why, err)
	}
}

func TestARemovalRefusesWhileTheTerminalsAreNotAllListed(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	g := turnsGuard{e: te.Env, snap: staticSnapshot{&wsdomain.Snapshot{}}}
	if why, err := g.Refusal(context.Background(), "ws1"); err != nil || why != notAllListed {
		t.Fatalf("refusal %q %v", why, err)
	}
}

func TestWorkspaceRemoveForgetsTheLookOfAWorkspaceThatLeft(t *testing.T) {
	t.Parallel()
	r := newRemovalRig(t, wt.Checkout{})
	r.s.looks["gone"] = removeLook{}
	r.check(t, true)
	if _, ok := r.s.looks["gone"]; ok {
		t.Fatal("kept the look of a workspace the manager no longer lists")
	}
	if _, ok := r.s.looks["ws1"]; !ok {
		t.Fatal("no look of ws1")
	}
}

type staticSnapshot struct{ s *wsdomain.Snapshot }

func (s staticSnapshot) Read(context.Context, wsdomain.Part) (*wsdomain.Snapshot, error) {
	return s.s, nil
}

func TestWorkspaceRemoveSkipsTheFirstRoundInTheBoundState(t *testing.T) {
	t.Parallel()
	r := newRemovalRig(t, wt.Checkout{})
	st := r.s.bindForTest(t)
	detail, err := r.s.Run(context.Background(), wfStep("workspace.remove"), r.u, workflow.Why{Workflow: "workspace-review-finished"}, automation.RunOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.mgr.Removed(), []string{"ws1"}) || !(st.Mark("acme/api#7", ghsource.MarkFirstSkipped) != "") {
		t.Fatalf("removed %v, skipped %v", r.mgr.Removed(), (st.Mark("acme/api#7", ghsource.MarkFirstSkipped) != ""))
	}
	if !strings.Contains(detail, "branch "+wt.Branch+" head ") {
		t.Fatalf("detail %q", detail)
	}
}

func TestWorkspaceUnregisterRefusesWhatItWouldRefuseToRun(t *testing.T) {
	t.Parallel()
	for want, edit := range map[string]func(w *wsdomain.Workspace){
		"not a local workspace": func(w *wsdomain.Workspace) { w.HostID, w.Path = "ssh:box", "/nonexistent/ws1" },
		"the main checkout":     func(w *wsdomain.Workspace) { w.IsMain, w.Path = true, "/nonexistent/ws1" },
	} {
		r := newRemovalRig(t, wt.Checkout{})
		edit(&r.mgr.Workspaces[0])
		r.u = wsdomain.UnitOf(r.mgr.Workspaces[0].Path)
		r.units.Put(r.u, workflow.Facts{"workspace.pr": "acme/api#7", "workspace.pr_state": "OPEN", "workspace.repo": "acme/api"})
		refusal, err := r.s.Check(context.Background(), wfStep("workspace.unregister"), r.u, workflow.Why{}, automation.CheckOpts{Observe: false})
		if err != nil || refusal != want {
			t.Errorf("check %q %v, want %q", refusal, err, want)
		}
	}
}

func TestWorkspaceUnregisterRefusesAnExistingPath(t *testing.T) {
	t.Parallel()
	r := newRemovalRig(t, wt.Checkout{})
	r.world.Files.(*wt.Files).Put(r.world.Dir+"/README", "x")
	refusal, err := r.s.Check(context.Background(), wfStep("workspace.unregister"), r.u, workflow.Why{}, automation.CheckOpts{Observe: false})
	if err != nil || refusal != "its path still exists" {
		t.Fatalf("check %q %v", refusal, err)
	}
}

type recordingPub struct {
	mu  sync.Mutex
	evs []port.Event
}

func (p *recordingPub) Publish(evs ...port.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.evs = append(p.evs, evs...)
}

// besideWorkflow writes files beside workflow's manifest in a profile
// directory te's config names.
func besideWorkflow(t *testing.T, te *testEnv, workflow string, files map[string]string) {
	t.Helper()
	dir := t.TempDir()
	wd := filepath.Join(dir, "workflows", workflow)
	if err := os.MkdirAll(wd, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(wd, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	te.cfg.Profile = dir
}

func TestNotifySelfPublishesItsText(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	besideWorkflow(t, te, "ping", map[string]string{"text.md.tmpl": "look at it", "named.md": "named text"})
	pub := &recordingPub{}
	units := automationtest.NewUnits()
	u := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	units.Put(u, workflow.Facts{})
	s := newStepSet(te.Env, stepDeps{Units: units, Rounds: te.Rounds, Pub: pub})
	if _, err := s.Run(context.Background(), wfStep("notify.self", "text", "named.md"), u, workflow.Why{Workflow: "ping"}, automation.RunOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Run(context.Background(), wfStep("notify.self"), u, workflow.Why{Workflow: "ping"}, automation.RunOpts{}); err != nil {
		t.Fatal(err)
	}
	if len(pub.evs) != 2 || pub.evs[0].Kind != kindNotifySelf || pub.evs[0].Data != "named text" || pub.evs[1].Data != "look at it" || pub.evs[0].Unit != u {
		t.Fatalf("published %+v", pub.evs)
	}
}

func TestNotifySelfRendersItsTemplate(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	besideWorkflow(t, te, "ping", map[string]string{"text.md.tmpl": `{{.Workflow}}: {{index .Facts "pr.slug"}} at {{.Unit}}`, "broken.md": "{{.Workflow"})
	pub := &recordingPub{}
	units := automationtest.NewUnits()
	u := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	units.Put(u, workflow.Facts{"pr.slug": "acme/api#7"})
	s := newStepSet(te.Env, stepDeps{Units: units, Rounds: te.Rounds, Pub: pub})
	if _, err := s.Run(context.Background(), wfStep("notify.self"), u, workflow.Why{Workflow: "ping"}, automation.RunOpts{}); err != nil {
		t.Fatal(err)
	}
	if len(pub.evs) != 1 || pub.evs[0].Data != "ping: acme/api#7 at "+u.String() {
		t.Fatalf("published %+v", pub.evs)
	}
	refusal, err := s.Check(context.Background(), wfStep("notify.self", "text", "broken.md"), u, workflow.Why{Workflow: "ping"}, automation.CheckOpts{})
	if err == nil && refusal == "" {
		t.Fatal("a template that does not parse passed the check")
	}
}

func TestNotifySelfNotifiesAGoneUnitWithNoFacts(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	besideWorkflow(t, te, "ping", map[string]string{"text.md.tmpl": `{{.Workflow}}: {{len .Facts}} facts at {{.Unit}}`})
	pub := &recordingPub{}
	s := newStepSet(te.Env, stepDeps{Units: automationtest.NewUnits(), Rounds: te.Rounds, Pub: pub})
	u := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	if _, err := s.Run(context.Background(), wfStep("notify.self"), u, workflow.Why{Workflow: "ping"}, automation.RunOpts{}); err != nil {
		t.Fatal(err)
	}
	if len(pub.evs) != 1 || pub.evs[0].Data != "ping: 0 facts at "+u.String() {
		t.Fatalf("published %+v", pub.evs)
	}
}

// blockingAsker answers once release closes, counting its calls.
type blockingAsker struct {
	mu      sync.Mutex
	calls   int
	release chan struct{}
	answer  string
}

func (a *blockingAsker) Ask(ctx context.Context, q turns.Ask) (turns.Answer, error) {
	a.mu.Lock()
	a.calls++
	a.mu.Unlock()
	<-a.release
	return turns.Answer{JSON: []byte(a.answer)}, nil
}

func (a *blockingAsker) n() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

type classifyRig struct {
	s     *stepSet
	units *automationtest.Units
	asker *blockingAsker
	db    *sqlite.DB
	u     unit.Unit
	st    workflow.Step
	why   workflow.Why
	te    *testEnv
}

func newClassifyRig(t *testing.T, limit int) *classifyRig {
	t.Helper()
	te := newTestEnv(t, &fake.Manager{})
	te.cfg.Classify.PerDay = limit
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "c.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := &classifyRig{units: automationtest.NewUnits(), asker: &blockingAsker{release: make(chan struct{}), answer: `{"kind":"bug","urgent":true}`}, db: db, te: te,
		u: unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}, st: wfStep("classify"),
		why: workflow.Why{Workflow: "triage-bugs", Trigger: workflow.TriggerEvent}}
	besideWorkflow(t, te, "triage-bugs", map[string]string{"prompt.md": "what is it", "schema.json": `{"type":"object"}`,
		"strict.json": `{"type":"object","additionalProperties":false,"required":["kind"],"properties":{"kind":{"type":"string","enum":["feature","chore"]},"urgent":{"type":"boolean"}}}`})
	r.units.Put(r.u, workflow.Facts{"pr.fingerprint": "fp1", "pr.repo": "acme/api"})
	r.s = newStepSet(te.Env, stepDeps{Units: r.units, Rounds: te.Rounds, Classify: classifyDeps{Store: db.Classifications(), Asker: r.asker}})
	t.Cleanup(func() {
		select {
		case <-r.asker.release:
		default:
			close(r.asker.release)
		}
		r.s.classify.wait()
	})
	return r
}

func (r *classifyRig) run(t *testing.T) (string, error) {
	t.Helper()
	return r.s.Run(context.Background(), r.st, r.u, r.why, automation.RunOpts{})
}

func TestClassifyNeverBlocksTheSweep(t *testing.T) {
	t.Parallel()
	r := newClassifyRig(t, 5)
	// The classifier answers only once released, so a Run that waits for it would hang; the watchdog only
	// ends such a hang, and the test then fails whatever the machine's load.
	hung := make(chan struct{})
	watchdog := time.AfterFunc(time.Minute, func() {
		close(r.asker.release)
		close(hung)
	})
	_, err := r.run(t)
	if !watchdog.Stop() {
		<-hung
		t.Fatal("Run waited for the classifier")
	}
	if !errors.As(err, new(automation.Deferred)) {
		t.Fatalf("run: %v", err)
	}
	if _, err := r.run(t); !errors.As(err, new(automation.Deferred)) {
		t.Fatalf("a second run while classifying: %v", err)
	}
	close(r.asker.release)
	r.s.classify.wait()
	if r.asker.n() != 1 {
		t.Fatalf("asked %d times", r.asker.n())
	}
	if spent, _ := r.db.Classifications().Spent(context.Background(), testNow); spent != 1 {
		t.Fatalf("spent %d", spent)
	}
	if _, err := r.run(t); err != nil {
		t.Fatalf("once classified: %v", err)
	}
}

func TestClassifyCachesByFingerprint(t *testing.T) {
	t.Parallel()
	r := newClassifyRig(t, 5)
	close(r.asker.release)
	if _, err := r.run(t); !errors.As(err, new(automation.Deferred)) {
		t.Fatalf("first run: %v", err)
	}
	r.s.classify.wait()
	if _, err := r.run(t); err != nil {
		t.Fatal(err)
	}
	units := classifiedUnits{Units: r.units, c: r.s.classify, workflows: func() []workflow.Workflow {
		return []workflow.Workflow{{Name: "triage-bugs", Provider: unit.GitHub, Slot: "pr", Steps: []workflow.Step{r.st}}}
	}}
	facts, err := units.Facts(context.Background(), r.u)
	if err != nil {
		t.Fatal(err)
	}
	if facts["classify.triage_bugs.kind"] != "bug" || facts["classify.triage_bugs.urgent"] != true {
		t.Fatalf("facts %v", facts)
	}
	r.units.Set(r.u, "pr.fingerprint", "fp2")
	if facts, _ := units.Facts(context.Background(), r.u); facts["classify.triage_bugs.kind"] != nil {
		t.Fatalf("another fingerprint read the old output: %v", facts)
	}
	if _, err := r.run(t); !errors.As(err, new(automation.Deferred)) {
		t.Fatalf("a new fingerprint: %v", err)
	}
	r.s.classify.wait()
	if r.asker.n() != 2 {
		t.Fatalf("asked %d times", r.asker.n())
	}
}

func TestClassifyStopsAtTheDailyCap(t *testing.T) {
	t.Parallel()
	r := newClassifyRig(t, 1)
	close(r.asker.release)
	if _, err := r.run(t); !errors.As(err, new(automation.Deferred)) {
		t.Fatalf("first run: %v", err)
	}
	r.s.classify.wait()
	r.units.Set(r.u, "pr.fingerprint", "fp2")
	refusal, err := r.s.Check(context.Background(), r.st, r.u, r.why, automation.CheckOpts{Observe: false})
	if err != nil || refusal != "daily classify cap 1 reached" {
		t.Fatalf("check %q %v", refusal, err)
	}
	_, err = r.run(t)
	var refused automation.Refused
	if !asRefused(err, &refused) || refused.Reason != "daily classify cap 1 reached" {
		t.Fatalf("run: %v", err)
	}
	if r.asker.n() != 1 {
		t.Fatalf("asked %d times", r.asker.n())
	}
	r.s.d.Classify.BreakerOpen = func() bool { return true }
	r.te.cfg.Classify.PerDay = 5
	if refusal, _ := r.s.Check(context.Background(), r.st, r.u, r.why, automation.CheckOpts{Observe: false}); refusal != "the brief breaker is open" {
		t.Fatalf("breaker: %q", refusal)
	}
}

// bindForTest loads the stored round state and binds it, as the engine's
// Load does.
func (s *stepSet) bindForTest(t *testing.T) *round.Store {
	t.Helper()
	st, err := s.states().Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return st.(*roundState).st
}

type failingAsker struct{ calls int }

func (a *failingAsker) Ask(context.Context, turns.Ask) (turns.Answer, error) {
	a.calls++
	return turns.Answer{}, errors.New("the answer does not fit the schema")
}

func TestAFailedClassifyIsAnAttemptNotAnotherCharge(t *testing.T) {
	t.Parallel()
	r := newClassifyRig(t, 5)
	asker := &failingAsker{}
	r.s.d.Classify.Asker = asker
	if _, err := r.run(t); !errors.As(err, new(automation.Deferred)) {
		t.Fatalf("first run: %v", err)
	}
	r.s.classify.wait()
	_, err := r.run(t)
	if err == nil || errors.As(err, new(automation.Deferred)) || errors.As(err, new(automation.Refused)) || errors.As(err, new(automation.Transient)) {
		t.Fatalf("after the failure: %v", err)
	}
	if spent, _ := r.db.Classifications().Spent(context.Background(), testNow); spent != 1 || asker.calls != 1 {
		t.Fatalf("spent %d, asked %d", spent, asker.calls)
	}
}

func TestAClassifyAnswerOutsideItsSchemaIsRefused(t *testing.T) {
	t.Parallel()
	r := newClassifyRig(t, 5)
	r.st = wfStep("classify", "schema", "strict.json")
	close(r.asker.release)
	if _, err := r.run(t); !errors.As(err, new(automation.Deferred)) {
		t.Fatalf("first run: %v", err)
	}
	r.s.classify.wait()
	if _, err := r.run(t); err == nil || !strings.Contains(err.Error(), "bug is not one of") {
		t.Fatalf("an answer outside the schema: %v", err)
	}
	units := classifiedUnits{Units: r.units, c: r.s.classify, workflows: func() []workflow.Workflow {
		return []workflow.Workflow{{Name: "triage-bugs", Provider: unit.GitHub, Slot: "pr", Steps: []workflow.Step{r.st}}}
	}}
	if facts, _ := units.Facts(context.Background(), r.u); facts["classify.triage_bugs.kind"] != nil {
		t.Fatalf("the refused answer became facts: %v", facts)
	}
}

func TestClassifyAsksAgainOnceItsPromptOrSchemaChanges(t *testing.T) {
	t.Parallel()
	r := newClassifyRig(t, 5)
	close(r.asker.release)
	if _, err := r.run(t); !errors.As(err, new(automation.Deferred)) {
		t.Fatalf("first run: %v", err)
	}
	r.s.classify.wait()
	if _, err := r.run(t); err != nil {
		t.Fatal(err)
	}
	units := classifiedUnits{Units: r.units, c: r.s.classify, workflows: func() []workflow.Workflow {
		return []workflow.Workflow{{Name: "triage-bugs", Provider: unit.GitHub, Slot: "pr", Steps: []workflow.Step{r.st}}}
	}}
	for i, files := range []map[string]string{
		{"prompt.md": "what is it, and is it urgent", "schema.json": `{"type":"object"}`},
		{"prompt.md": "what is it, and is it urgent", "schema.json": `{"type":"object","properties":{"kind":{"type":"string"}}}`},
	} {
		besideWorkflow(t, r.te, "triage-bugs", files)
		if facts, _ := units.Facts(context.Background(), r.u); facts["classify.triage_bugs.kind"] != nil {
			t.Fatalf("change %d: an output asked with another prompt or schema still reads: %v", i, facts)
		}
		if _, err := r.run(t); !errors.As(err, new(automation.Deferred)) {
			t.Fatalf("change %d: %v, want it asked again", i, err)
		}
		r.s.classify.wait()
		if _, err := r.run(t); err != nil {
			t.Fatalf("change %d, asked again: %v", i, err)
		}
	}
	if r.asker.n() != 3 {
		t.Fatalf("asked %d times, want once per prompt and schema", r.asker.n())
	}
}
