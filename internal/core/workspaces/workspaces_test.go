package workspaces_test

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/workspaces"
	wt "github.com/wloophq/wloop/internal/core/workspaces/workspacestest"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workspace"
)

var (
	ctx    = context.Background()
	prUnit = unit.Unit{Provider: unit.GitHub, Key: "acme/api#1"}
)

type fixture struct {
	s        *workspaces.Service
	m        *wt.Manager
	git      *wt.Git
	files    *wt.Files
	sessions *wt.Sessions
	rounds   *wt.Rounds
	fetched  *wt.FetchLog
	pub      *wt.Publisher
	w        workspace.Workspace
	pr       workspaces.PRInfo
}

func newFixture(t *testing.T, c wt.Checkout) *fixture {
	t.Helper()
	world := wt.FakeWorld(t, c)
	s, m := wt.Service(world)
	f := &fixture{s: s, m: m, git: world.Remote.(*wt.Git), files: world.Files.(*wt.Files),
		sessions: s.Sessions.(*wt.Sessions), rounds: s.Rounds.(*wt.Rounds), fetched: s.Fetched.(*wt.FetchLog), pub: s.Pub.(*wt.Publisher),
		w:  workspace.Workspace{ID: "ws-1", RepoID: "r1", Path: world.Dir, Branch: wt.Branch, HostID: workspace.LocalHost},
		pr: workspaces.PRInfo{Unit: prUnit, Number: 1}}
	if c.PRHoldsHead {
		f.pr.Head = world.Head
	}
	return f
}

// fetched is newFixture after today's fetch.
func fetched(t *testing.T, c wt.Checkout) *fixture {
	t.Helper()
	f := newFixture(t, c)
	if err := f.s.Fetch(ctx, f.w, f.pr); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) refusal(t *testing.T) (string, string) {
	t.Helper()
	r, backup, err := f.s.RemoveRefusal(ctx, f.w, f.pr)
	if err != nil {
		t.Fatal(err)
	}
	return r, backup
}

// wantRemoveRefused runs Remove and wants a refusal with want and nothing
// removed.
func (f *fixture) wantRemoveRefused(t *testing.T, force bool, want string) workspaces.Refusal {
	t.Helper()
	err := f.s.Remove(ctx, f.w, f.pr, force)
	var r workspaces.Refusal
	if !errors.As(err, &r) || !strings.Contains(r.Reason, want) {
		t.Fatalf("Remove = %v; want a refusal with %q", err, want)
	}
	if len(f.m.Removed()) != 0 || len(f.pub.Events()) != 0 || len(f.rounds.Skipped()) != 0 {
		t.Fatalf("a refused removal acted: removed %v, events %v, skipped %v", f.m.Removed(), f.pub.Events(), f.rounds.Skipped())
	}
	return r
}

func TestFakeTree(t *testing.T)     { wt.RunTree(t, wt.FakeWorld) }
func TestFakeRemote(t *testing.T)   { wt.RunRemote(t, wt.FakeWorld) }
func TestFakeFiles(t *testing.T)    { wt.RunFiles(t, wt.FakeWorld) }
func TestFakeRemovals(t *testing.T) { wt.RunRemovals(t, wt.FakeWorld) }

func TestTheMainCheckoutIsNeverRemoved(t *testing.T) {
	f := fetched(t, wt.Checkout{})
	f.w.IsMain = true
	if r, _ := f.refusal(t); r != "the main checkout" {
		t.Fatalf("refusal %q", r)
	}
	f.wantRemoveRefused(t, false, "the main checkout")
	f.wantRemoveRefused(t, true, "the main checkout")
	f.w.Path = "/gone"
	if err := f.s.Unregister(ctx, f.w); err == nil || len(f.m.Unregistered()) != 0 {
		t.Fatalf("Unregister of the main checkout: %v, %v", err, f.m.Unregistered())
	}
}

func TestTheBackupCommandQuotesABranchTheShellWouldRead(t *testing.T) {
	f := newFixture(t, wt.Checkout{Unpushed: 1})
	f.git.Dirs[f.w.Path].Remote = "fork"
	f.w.Branch = "x$(touch pwned);'q"
	if err := f.s.Fetch(ctx, f.w, f.pr); err != nil {
		t.Fatal(err)
	}
	if _, backup := f.refusal(t); backup != `git push fork 'HEAD:refs/heads/user/backup-x$(touch pwned);'\''q'` {
		t.Fatalf("backup %s", backup)
	}
}

func TestUnpushedCommitsBlockWithTheBackupCommandOnTheOwnRemote(t *testing.T) {
	f := newFixture(t, wt.Checkout{Unpushed: 3})
	f.git.Dirs[f.w.Path].Remote = "fork"
	if err := f.s.Fetch(ctx, f.w, f.pr); err != nil {
		t.Fatal(err)
	}
	r, backup := f.refusal(t)
	if !strings.Contains(r, "3 commits not pushed") || backup != "git push fork HEAD:refs/heads/user/backup-feat" {
		t.Fatalf("refusal %q, backup %q", r, backup)
	}
	refused := f.wantRemoveRefused(t, false, "3 commits not pushed")
	if refused.Backup != backup || !strings.Contains(refused.Error(), backup) {
		t.Fatalf("Remove's refusal %+v", refused)
	}
}

func TestAFetchFailureRefusesRemove(t *testing.T) {
	f := newFixture(t, wt.Checkout{})
	f.git.FetchErr = errors.New("could not read from remote repository")
	if err := f.s.Fetch(ctx, f.w, f.pr); err == nil || f.fetched.Marks() != 0 {
		t.Fatalf("a failed fetch: %v, %d marks", err, f.fetched.Marks())
	}
	f.wantRemoveRefused(t, false, "could not fetch: could not read from remote repository")
	if f.fetched.Marks() != 0 {
		t.Fatal("Remove marked a failed fetch")
	}
}

func TestRemoveRefusalNeverFetchesOrWrites(t *testing.T) {
	for _, r := range wt.Removals {
		f := newFixture(t, r.Checkout)
		f.pr.Open = true
		for range 2 {
			if _, _, err := f.s.RemoveRefusal(ctx, f.w, f.pr); err != nil {
				t.Fatal(err)
			}
			if _, _, err := f.s.RemoveRefusalAsFetched(ctx, f.w, f.pr); err != nil {
				t.Fatal(err)
			}
		}
		if len(f.git.Fetches()) != 0 || f.fetched.Marks() != 0 || len(f.m.Removed()) != 0 || len(f.pub.Events()) != 0 ||
			len(f.sessions.Closed()) != 0 || len(f.rounds.Skipped()) != 0 {
			t.Errorf("%s: fetches %v, marks %d, removed %v, events %v, closed %v, skipped %v", r.Name, f.git.Fetches(), f.fetched.Marks(),
				f.m.Removed(), f.pub.Events(), f.sessions.Closed(), f.rounds.Skipped())
		}
	}
}

func TestNotFetchedTodayRefuses(t *testing.T) {
	f := newFixture(t, wt.Checkout{})
	if r, _ := f.refusal(t); r != workspaces.NotFetchedToday {
		t.Fatalf("never fetched: %q", r)
	}
	yesterday := f.s.Clock.Now().Add(-16 * time.Hour)
	if err := f.fetched.Mark(ctx, f.w.ID, yesterday); err != nil {
		t.Fatal(err)
	}
	if r, _ := f.refusal(t); r != workspaces.NotFetchedToday {
		t.Fatalf("fetched yesterday: %q", r)
	}
	if r, _, err := f.s.RemoveRefusalAsFetched(ctx, f.w, f.pr); err != nil || r != "" {
		t.Fatalf("as fetched: %q, %v", r, err)
	}
	f.s.Fetched = nil
	if r, _ := f.refusal(t); r != workspaces.NotFetchedToday {
		t.Fatalf("no fetch log: %q", r)
	}
}

func TestFetchMarksTheDay(t *testing.T) {
	f := newFixture(t, wt.Checkout{})
	if err := f.s.Fetch(ctx, f.w, f.pr); err != nil {
		t.Fatal(err)
	}
	if last, _ := f.fetched.Last(ctx, f.w.ID); !last.Equal(f.s.Clock.Now()) || f.fetched.Marks() != 1 {
		t.Fatalf("last %v, %d marks", last, f.fetched.Marks())
	}
	if r, _ := f.refusal(t); r != "" {
		t.Fatalf("refusal %q after today's fetch", r)
	}
	if err := f.s.Remove(ctx, f.w, f.pr, false); err != nil || f.fetched.Marks() != 1 || len(f.git.Fetches()) != 2 {
		t.Fatalf("Remove: %v, %d marks, fetches %v; Remove fetches again and marks nothing", err, f.fetched.Marks(), f.git.Fetches())
	}
}

func TestAnIgnoredFileIdenticalToTheMainCheckoutIsDisposable(t *testing.T) {
	f := fetched(t, wt.Checkout{Ignored: map[string]string{"credential.json": `{"k":1}`}, Main: map[string]string{"credential.json": `{"k":1}`}})
	if r, _ := f.refusal(t); r != "" {
		t.Fatalf("refusal %q", r)
	}
	if err := f.s.Remove(ctx, f.w, f.pr, false); err != nil {
		t.Fatal(err)
	}
}

func TestAnIgnoredDirectoryIdenticalToMainIsDisposable(t *testing.T) {
	c := wt.Checkout{Main: map[string]string{"cache/a": "1", "cache/d/b": "2"}}
	f := fetched(t, c)
	f.git.Dirs[f.w.Path].Ignored = []string{"cache/"}
	f.files.Put(f.w.Path+"/cache/a", "1")
	f.files.Put(f.w.Path+"/cache/d/b", "2")
	if r, _ := f.refusal(t); r != "" {
		t.Fatalf("a directory entry identical to main: %q", r)
	}
	f.files.Put(f.w.Path+"/cache/d/c", "3")
	if r, _ := f.refusal(t); r != "untracked or ignored files outside the disposable list: cache/" {
		t.Fatalf("a directory with an extra file: %q", r)
	}
}

func TestAnIgnoredFileThatDiffersFromTheMainCheckoutBlocks(t *testing.T) {
	f := fetched(t, wt.Checkout{Ignored: map[string]string{"credential.json": `{"k":1}`, "cache/a": "1"}, Main: map[string]string{"credential.json": `{"k":2}`, "cache/a": "1"}})
	r, _ := f.refusal(t)
	if r != "untracked or ignored files outside the disposable list: credential.json" {
		t.Fatalf("refusal %q", r)
	}
	refused := f.wantRemoveRefused(t, false, "credential.json")
	if !slices.Equal(refused.Paths, []string{"credential.json"}) {
		t.Fatalf("paths %v", refused.Paths)
	}
}

func TestAnUntrackedFileOutsideTheListBlocks(t *testing.T) {
	f := fetched(t, wt.Checkout{Untracked: map[string]string{"threads.txt": "notes"}})
	if r, _ := f.refusal(t); r != "untracked or ignored files outside the disposable list: threads.txt" {
		t.Fatalf("refusal %q", r)
	}
	f.wantRemoveRefused(t, false, "threads.txt")
}

func TestALongListShowsTheFirstTen(t *testing.T) {
	ignored := map[string]string{}
	for _, c := range "abcdefghijkl" {
		ignored["secret-"+string(c)] = "x"
	}
	f := fetched(t, wt.Checkout{Ignored: ignored})
	r, _ := f.refusal(t)
	if !strings.HasSuffix(r, "secret-j, and 2 more") || strings.Contains(r, "secret-k") {
		t.Fatalf("refusal %q", r)
	}
}

func TestANestedRepoWithChangesBlocks(t *testing.T) {
	f := fetched(t, wt.Checkout{Nested: map[string]wt.Nested{"op-web": {Modified: true}}})
	if r, _ := f.refusal(t); r != "untracked or ignored files outside the disposable list: op-web/" {
		t.Fatalf("refusal %q", r)
	}
	f = fetched(t, wt.Checkout{Nested: map[string]wt.Nested{"op-web": {Unpushed: 2}}})
	f.wantRemoveRefused(t, false, "op-web/")
	for name, n := range map[string]wt.Nested{
		"an ignored file":                 {Ignored: map[string]string{".env": "K=1"}},
		"a commit on another branch":      {OtherBranch: true},
		"a stash":                         {Stash: true},
		"a detached commit":               {Detached: true},
		"no remote":                       {NoRemote: true},
		"changes under a disposable name": {Under: "build", Modified: true},
	} {
		f = fetched(t, wt.Checkout{Nested: map[string]wt.Nested{"op-web": n}})
		want := "untracked or ignored files outside the disposable list: " + path.Join(n.Under, "op-web") + "/"
		if r, _ := f.refusal(t); r != want {
			t.Errorf("%s: refusal %q", name, r)
		}
	}
	f = fetched(t, wt.Checkout{Nested: map[string]wt.Nested{"op-web": {Ignored: map[string]string{"node_modules/x.js": ""}}}})
	if r, _ := f.refusal(t); r != "" {
		t.Fatalf("a clean nested repository: %q", r)
	}
}

func TestADeletedRemoteBranchWithLocalCommitsBlocks(t *testing.T) {
	f := fetched(t, wt.Checkout{BranchGone: true})
	r, backup := f.refusal(t)
	if !strings.Contains(r, "its branch feat is gone from origin") || backup == "" {
		t.Fatalf("refusal %q, backup %q", r, backup)
	}
	f.wantRemoveRefused(t, false, "is gone from origin")
}

func TestASquashMergedBranchThePRHoldsIsRemovable(t *testing.T) {
	f := fetched(t, wt.Checkout{BranchGone: true, PRHoldsHead: true, Unpushed: 1})
	if r, _ := f.refusal(t); r != "" {
		t.Fatalf("refusal %q", r)
	}
	if err := f.s.Remove(ctx, f.w, f.pr, false); err != nil || !slices.Equal(f.m.Removed(), []string{"ws-1"}) {
		t.Fatalf("Remove: %v, removed %v", err, f.m.Removed())
	}
}

func TestACommitBetweenCheckAndRemoveRefuses(t *testing.T) {
	f := fetched(t, wt.Checkout{})
	reads := 0
	f.git.AfterUnpushed = func(dir string, d *wt.Dir) {
		if reads++; reads == 1 {
			d.Unpushed++
		}
	}
	f.wantRemoveRefused(t, false, "changed while it was checked")
}

func TestAnAmendBetweenCheckAndRemoveRefuses(t *testing.T) {
	f := fetched(t, wt.Checkout{})
	f.s.Sessions = &amending{Sessions: f.sessions, d: f.git.Dirs[f.w.Path]}
	f.wantRemoveRefused(t, false, "changed while it was checked")
}

// amending rewrites HEAD while the sessions close, as the user amending in a
// terminal then would: the unpushed count stays.
type amending struct {
	*wt.Sessions
	d *wt.Dir
}

func (a *amending) CloseAll(ctx context.Context, id, reason string) error {
	a.d.Head = "amended"
	return a.Sessions.CloseAll(ctx, id, reason)
}

func TestAFileWrittenWhileTheSessionsCloseRefuses(t *testing.T) {
	for name, write := range map[string]func(d *wt.Dir){
		"modified":  func(d *wt.Dir) { d.Modified = 1 },
		"untracked": func(d *wt.Dir) { d.Untracked = append(d.Untracked, "threads.txt") },
		"ignored":   func(d *wt.Dir) { d.Ignored = append(d.Ignored, ".env") },
	} {
		t.Run(name, func(t *testing.T) {
			f := fetched(t, wt.Checkout{})
			f.s.Sessions = &flushing{Sessions: f.sessions, d: f.git.Dirs[f.w.Path], write: write}
			f.wantRemoveRefused(t, false, "changed while it was checked")
		})
	}
}

// flushing writes into the checkout while the sessions close, as an agent
// saving its work on the way out would.
type flushing struct {
	*wt.Sessions
	d     *wt.Dir
	write func(d *wt.Dir)
}

func (f *flushing) CloseAll(ctx context.Context, id, reason string) error {
	f.write(f.d)
	return f.Sessions.CloseAll(ctx, id, reason)
}

func TestFetchAndRemoveFetchTheSamePullRequest(t *testing.T) {
	f := newFixture(t, wt.Checkout{})
	f.pr.Number = 7
	if err := f.s.Fetch(ctx, f.w, f.pr); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Remove(ctx, f.w, f.pr, false); err != nil {
		t.Fatal(err)
	}
	if want := []string{"/w/ws#7", "/w/ws#7"}; !slices.Equal(f.git.Fetches(), want) {
		t.Fatalf("fetches %v, want %v", f.git.Fetches(), want)
	}
}

func TestALiveSessionTheGuardRefusesBlocks(t *testing.T) {
	f := fetched(t, wt.Checkout{})
	f.sessions.Refuse = map[string]string{"ws-1": "you typed in it after the last turn"}
	if r, _ := f.refusal(t); r != "you typed in it after the last turn" {
		t.Fatalf("refusal %q", r)
	}
	f.wantRemoveRefused(t, false, "you typed in it")
	if len(f.sessions.Closed()) != 0 {
		t.Fatalf("closed %v", f.sessions.Closed())
	}
}

// orderedSessions records whether the manager had removed anything when
// the sessions were closed.
type orderedSessions struct {
	*wt.Sessions
	m          *wt.Manager
	removedYet bool
}

func (o *orderedSessions) CloseAll(ctx context.Context, id, reason string) error {
	o.removedYet = len(o.m.Removed()) > 0
	return o.Sessions.CloseAll(ctx, id, reason)
}

func TestRemoveClosesClosableSessionsWithTheirResumeLine(t *testing.T) {
	f := fetched(t, wt.Checkout{})
	o := &orderedSessions{Sessions: f.sessions, m: f.m}
	f.s.Sessions = o
	if err := f.s.Remove(ctx, f.w, f.pr, false); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.sessions.Closed(), []string{"ws-1: its workspace is removed"}) || o.removedYet {
		t.Fatalf("closed %v, after the removal: %v", f.sessions.Closed(), o.removedYet)
	}
	f = fetched(t, wt.Checkout{})
	f.sessions.CloseErr = errors.New("terminal busy")
	if err := f.s.Remove(ctx, f.w, f.pr, false); err == nil || len(f.m.Removed()) != 0 {
		t.Fatalf("a failed close: %v, removed %v", err, f.m.Removed())
	}
}

func TestRemovingAnOpenPRsWorkspaceSkipsItsFirstRound(t *testing.T) {
	f := fetched(t, wt.Checkout{})
	f.pr.Open = true
	if err := f.s.Remove(ctx, f.w, f.pr, false); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.rounds.Skipped(), []unit.Unit{prUnit}) {
		t.Fatalf("skipped %v", f.rounds.Skipped())
	}
	f = fetched(t, wt.Checkout{})
	if err := f.s.Remove(ctx, f.w, f.pr, false); err != nil || len(f.rounds.Skipped()) != 0 {
		t.Fatalf("a finished pull request: %v, skipped %v", err, f.rounds.Skipped())
	}
	f = fetched(t, wt.Checkout{Unpushed: 1})
	f.pr.Open = true
	f.wantRemoveRefused(t, false, "not pushed")
}

func TestForceSkipsTheChecksButNotTheRecord(t *testing.T) {
	f := newFixture(t, wt.Checkout{Unpushed: 3, Untracked: map[string]string{"threads.txt": "x"}})
	f.git.FetchErr = errors.New("offline")
	f.sessions.Refuse = map[string]string{"ws-1": "its agent is working"}
	f.pr.Open = true
	if err := f.s.Remove(ctx, f.w, f.pr, true); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.m.Forced(), []string{"ws-1"}) || len(f.git.Fetches()) != 0 || len(f.pub.Events()) != 1 || len(f.rounds.Skipped()) != 1 {
		t.Fatalf("forced %v, fetches %v, events %v, skipped %v", f.m.Forced(), f.git.Fetches(), f.pub.Events(), f.rounds.Skipped())
	}
}

func TestUnregisterRefusesAnExistingPath(t *testing.T) {
	f := newFixture(t, wt.Checkout{Untracked: map[string]string{"threads.txt": "x"}})
	err := f.s.Unregister(ctx, f.w)
	var r workspaces.Refusal
	if !errors.As(err, &r) || r.Reason != "its path still exists" || len(f.m.Unregistered()) != 0 {
		t.Fatalf("Unregister = %v, unregistered %v", err, f.m.Unregistered())
	}
	f.w.Path = "/w/gone"
	if err := f.s.Unregister(ctx, f.w); err != nil || !slices.Equal(f.m.Unregistered(), []string{"ws-1"}) {
		t.Fatalf("Unregister = %v, unregistered %v", err, f.m.Unregistered())
	}
	evs := f.pub.Events()
	if len(evs) != 1 || evs[0].Kind != workspaces.KindUnregistered || evs[0].Data != "branch feat head unknown" || evs[0].Key != "ws-1" {
		t.Fatalf("events %+v", evs)
	}
}

func TestUnregisterRefusesARemoteOrPathlessWorkspace(t *testing.T) {
	for name, edit := range map[string]func(w *workspace.Workspace){
		"another host":  func(w *workspace.Workspace) { w.HostID = "ssh:box"; w.Path = "/w/gone" },
		"no host":       func(w *workspace.Workspace) { w.HostID = ""; w.Path = "/w/gone" },
		"no path":       func(w *workspace.Workspace) { w.Path = "" },
		"relative path": func(w *workspace.Workspace) { w.Path = "w/gone" },
	} {
		f := newFixture(t, wt.Checkout{})
		edit(&f.w)
		var r workspaces.Refusal
		if err := f.s.Unregister(ctx, f.w); !errors.As(err, &r) || len(f.m.Unregistered()) != 0 {
			t.Errorf("%s: Unregister = %v, unregistered %v", name, err, f.m.Unregistered())
		}
		if refusal, _, err := f.s.RemoveRefusal(ctx, f.w, f.pr); err != nil || refusal == "" {
			t.Errorf("%s: RemoveRefusal = %q, %v", name, refusal, err)
		}
		if err := f.s.Remove(ctx, f.w, f.pr, true); !errors.As(err, &r) || len(f.m.Removed()) != 0 {
			t.Errorf("%s: forced Remove = %v, removed %v", name, err, f.m.Removed())
		}
		if err := f.s.Fetch(ctx, f.w, f.pr); !errors.As(err, &r) || len(f.git.Fetches()) != 0 || f.fetched.Marks() != 0 {
			t.Errorf("%s: Fetch = %v, fetches %v", name, err, f.git.Fetches())
		}
	}
}

func TestRemoveRecordsBranchAndHead(t *testing.T) {
	f := fetched(t, wt.Checkout{})
	if err := f.s.Remove(ctx, f.w, f.pr, false); err != nil {
		t.Fatal(err)
	}
	evs := f.pub.Events()
	want := unit.Unit{Provider: unit.Workspace, Key: "/w/ws"}
	if len(evs) != 1 || evs[0].Kind != workspaces.KindRemoved || evs[0].Data != "branch feat head c0ffee" || evs[0].Unit != want ||
		evs[0].Key != "ws-1" || !evs[0].At.Equal(f.s.Clock.Now()) {
		t.Fatalf("events %+v", evs)
	}
}

func TestDisposableComesFromConfig(t *testing.T) {
	ignored := map[string]string{"node_modules/x.js": "", turn.ScratchDir + "/turn-abc.json": "", ".DS_Store": "", "__pycache__/m.pyc": ""}
	f := fetched(t, wt.Checkout{Ignored: ignored, Untracked: map[string]string{"threads.txt": "x"}})
	f.s.Disposable = func() []string { return config.Defaults().Cleanup.Disposable }
	if r, _ := f.refusal(t); r != "untracked or ignored files outside the disposable list: threads.txt" {
		t.Fatalf("the default list: %q", r)
	}
	f.s.Disposable = func() []string { return []string{"threads.txt"} }
	r, _ := f.refusal(t)
	if strings.Contains(r, "threads.txt") || !strings.Contains(r, "node_modules/") || !strings.Contains(r, turn.ScratchDir+"/") {
		t.Fatalf("a configured list replaces the default: %q", r)
	}
	f = fetched(t, wt.Checkout{Ignored: map[string]string{".looper/notes": ""}})
	f.s.Disposable = func() []string { return config.Defaults().Cleanup.Disposable }
	if r, _ := f.refusal(t); !strings.Contains(r, ".looper/") {
		t.Fatalf(".looper is no scratch directory: %q", r)
	}
}

func TestADirectoryWithoutGitIsNotACheckout(t *testing.T) {
	f := newFixture(t, wt.Checkout{})
	f.w.Path = "/w/main/.claude/worktrees/x"
	f.files.Put(f.w.Path+"/notes.md", "draft")
	if r, _ := f.refusal(t); r != workspaces.NotACheckout {
		t.Errorf("RemoveRefusal = %q, want %q", r, workspaces.NotACheckout)
	}
	if err := f.s.Fetch(ctx, f.w, f.pr); !strings.Contains(fmt.Sprint(err), workspaces.NotACheckout) {
		t.Errorf("Fetch = %v", err)
	}
	f.wantRemoveRefused(t, false, workspaces.NotACheckout)
	f.wantRemoveRefused(t, true, workspaces.NotACheckout)
	if got := f.git.Fetches(); len(got) != 0 {
		t.Errorf("fetched %v", got)
	}
}
