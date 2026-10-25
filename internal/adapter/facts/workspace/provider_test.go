package factworkspace_test

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

	factworkspace "github.com/wloophq/wloop/internal/adapter/facts/workspace"
	"github.com/wloophq/wloop/internal/adapter/facts/workspace/factworkspacetest"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/automation/automationtest"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/workspaces/workspacestest"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/gitx"
	"github.com/wloophq/wloop/internal/meter"
)

// Monday 2026-10-19 10:00 UTC.
var now = time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)

const repoID = "repo-1"

type world struct {
	snap      workspace.Snapshot
	rows      []dinv.PR
	viewer    string
	overrides map[unit.Unit]dinv.Override
	books     map[unit.Unit]turn.Book
	git       *fakeGit
	disk      *factworkspacetest.Disk
	gen       port.Generation
	cfg       config.Config
	now       time.Time
}

func newWorld() *world {
	cfg := config.Defaults()
	cfg.Cleanup.Disposable = []string{"node_modules", ".DS_Store"}
	disk := factworkspacetest.NewDisk()
	disk.Put("/w/main", time.Time{})
	disk.Put("/w/main/.git", time.Time{})
	return &world{
		snap: workspace.Snapshot{
			Gen:            3,
			Repos:          []workspace.Repo{{ID: repoID, Path: "/w/main", RemoteKey: "github.com/acme/api", DefaultBranch: "main"}},
			Workspaces:     []workspace.Workspace{{ID: "main", RepoID: repoID, Path: "/w/main", Branch: "main", HostID: workspace.LocalHost, IsMain: true}},
			WorkspaceScope: workspace.Scope{Complete: true},
			TerminalScope:  workspace.Scope{Complete: true},
			Loaded:         workspace.PartAll,
		},
		overrides: map[unit.Unit]dinv.Override{},
		books:     map[unit.Unit]turn.Book{},
		git:       &fakeGit{dirs: map[string]*workspacestest.Dir{"/w/main": {Branch: "main", Head: "m0"}}},
		disk:      disk,
		gen:       41,
		cfg:       cfg,
		now:       now,
	}
}

// add lists a local workspace at path with a checkout on branch.
func (w *world) add(ws workspace.Workspace) *workspacestest.Dir {
	ws.RepoID = repoID
	ws.HostID = workspace.LocalHost
	w.snap.Workspaces = append(w.snap.Workspaces, ws)
	d := &workspacestest.Dir{Branch: ws.Branch, Head: "h-" + ws.ID}
	w.git.dirs[ws.Path] = d
	w.disk.Put(ws.Path, now.Add(-time.Hour))
	w.disk.Put(ws.Path+"/.git", time.Time{})
	return d
}

// unit is the unit of the workspace id lists.
func (w *world) unit(id string) unit.Unit {
	for _, ws := range w.snap.Workspaces {
		if ws.ID == id {
			return workspace.UnitOf(ws.Path)
		}
	}
	return workspace.UnitOf("/absent/" + id)
}

func ref(n int) pr.Ref { return pr.Ref{Owner: "acme", Repo: "api", Number: n} }

func (w *world) provider() *factworkspace.Provider { return factworkspace.New(w.deps()) }

func (w *world) deps() factworkspace.Deps {
	return factworkspace.Deps{
		Snapshot:  snapshot{&w.snap},
		PRs:       viewerRows{rows(w.rows), w.viewer},
		Overrides: overrides(w.overrides),
		Books:     books(w.books),
		Git:       w.git,
		Disk:      w.disk,
		Changes:   gen{w},
		Cfg:       func() config.Config { return w.cfg },
		Clock:     clock{w},
	}
}

func (w *world) facts(t *testing.T, id string) workflow.Facts {
	t.Helper()
	f, err := w.provider().Facts(context.Background(), w.unit(id))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestUnitsContract(t *testing.T) {
	automationtest.RunUnits(t, unit.Workspace, func(*testing.T) automation.Units {
		w := newWorld()
		w.add(workspace.Workspace{ID: "ws-1", Path: "/w/ws-1", Branch: "feat"})
		w.rows = []dinv.PR{{Ref: ref(7), State: "OPEN", HeadBranch: "feat"}}
		return w.provider()
	})
}

func TestTemporaryPaths(t *testing.T) {
	for path, want := range map[string]bool{
		"/tmp/x":                          true,
		"/private/tmp/claude/x":           true,
		"/u/p/api/.claude/worktrees/feat": true,
		"/u/p/api-feat":                   false,
		"/tmpfoo/x":                       false,
		"/u/p/claude/worktrees/feat":      false,
	} {
		w := newWorld()
		w.add(workspace.Workspace{ID: "ws", Path: path, Branch: "feat"})
		if got := w.facts(t, "ws")["workspace.temporary"]; got != want {
			t.Errorf("temporary(%s) = %v, want %v", path, got, want)
		}
	}
}

func TestDuplicateIsTheUnlinkedOne(t *testing.T) {
	w := newWorld()
	w.add(workspace.Workspace{ID: "linked", Path: "/w/linked", Branch: "feat", LinkedPR: 7})
	w.add(workspace.Workspace{ID: "copy", Path: "/w/copy", Branch: "feat"})
	w.add(workspace.Workspace{ID: "alone", Path: "/w/alone", Branch: "other"})
	w.rows = []dinv.PR{{Ref: ref(7), State: "OPEN", HeadBranch: "feat"}, {Ref: ref(8), State: "OPEN", HeadBranch: "other"}}
	for id, want := range map[string]bool{"linked": false, "copy": true, "alone": false, "main": false} {
		f := w.facts(t, id)
		if f["workspace.duplicate"] != want {
			t.Errorf("%s: duplicate = %v, want %v", id, f["workspace.duplicate"], want)
		}
	}
	if got := w.facts(t, "copy")["workspace.pr"]; got != "acme/api#7" {
		t.Errorf("copy: pr = %v", got)
	}
}

func TestFinishedForCountsWorkingTime(t *testing.T) {
	w := newWorld()
	w.cfg.Cleanup.WorkspaceGrace = config.Days(24 * time.Hour)
	w.add(workspace.Workspace{ID: "ws", Path: "/w/ws", Branch: "feat", LinkedPR: 7})
	friday := time.Date(2026, 10, 16, 18, 0, 0, 0, time.UTC)
	w.rows = []dinv.PR{{Ref: ref(7), State: "MERGED", HeadBranch: "feat", MergedAt: friday, ClosedAt: friday.Add(-time.Minute)}}
	p := w.provider()
	f, err := p.Facts(context.Background(), unit.Unit{Provider: unit.Workspace, Key: "/w/ws"})
	if err != nil {
		t.Fatal(err)
	}
	if f["workspace.pr_state"] != "merged" {
		t.Errorf("pr_state = %v", f["workspace.pr_state"])
	}
	if got := f["workspace.pr_finished_for"]; got != 16*time.Hour {
		t.Errorf("pr_finished_for = %v, want 16h (Friday 18:00 to Monday 10:00, weekend out)", got)
	}
	if want := time.Date(2026, 10, 19, 18, 0, 0, 0, time.UTC); !p.Due().Equal(want) {
		t.Errorf("Due = %v, want the end of the grace %v", p.Due(), want)
	}
}

func TestDueIncludesTheStaleWorkspaceDeadline(t *testing.T) {
	w := newWorld()
	w.cfg.Cleanup.StaleWorkspace = config.Days(14 * 24 * time.Hour)
	w.add(workspace.Workspace{ID: "ws", Path: "/w/ws", Branch: "spike"})
	w.disk.SetTouched("/w/ws", now.Add(-10*24*time.Hour))
	p := w.provider()
	f, err := p.Facts(context.Background(), unit.Unit{Provider: unit.Workspace, Key: "/w/ws"})
	if err != nil {
		t.Fatal(err)
	}
	if f["workspace.quiet_for"] != 10*24*time.Hour {
		t.Errorf("quiet_for = %v", f["workspace.quiet_for"])
	}
	if want := now.Add(4 * 24 * time.Hour); !p.Due().Equal(want) {
		t.Errorf("Due = %v, want %v", p.Due(), want)
	}
}

func TestPendencyListsEachMechanicalCase(t *testing.T) {
	u := ref(7).Unit()
	cases := []struct {
		name   string
		dir    workspacestest.Dir
		book   []turn.Turn
		answer *turn.Answer
		want   []string
	}{
		{name: "clean", want: []string{}},
		{name: "modified", dir: workspacestest.Dir{Modified: 2}, want: []string{"modified files"}},
		{name: "untracked", dir: workspacestest.Dir{Untracked: []string{"notes.txt", "node_modules/x", "a/.DS_Store"}}, want: []string{"untracked files"}},
		{name: "disposable only", dir: workspacestest.Dir{Untracked: []string{"node_modules/x", "a.pyc"}, Ignored: []string{"secret.json"}}, want: []string{}},
		{name: "unpushed", dir: workspacestest.Dir{Unpushed: 1}, want: []string{"commits on no remote"}},
		{name: "asked", book: []turn.Turn{{Seq: 1, State: turn.Asked}}, want: []string{"turn asked unanswered"}},
		{name: "asked and answered", book: []turn.Turn{{Seq: 1, State: turn.Asked}}, answer: &turn.Answer{TurnSeq: 1, Route: turn.Queued, At: now}, want: []string{"turn open"}},
		{name: "failed", book: []turn.Turn{{Seq: 1, State: turn.Failed}}, want: []string{"turn failed"}},
		{name: "unrecorded", book: []turn.Turn{{Seq: 1, State: turn.Unrecorded}}, want: []string{"turn unrecorded"}},
		{name: "older open turn", book: []turn.Turn{{Seq: 1, State: turn.Started}, {Seq: 2, State: turn.Done}}, want: []string{"turn open"}},
		{name: "done", book: []turn.Turn{{Seq: 1, State: turn.Done}}, want: []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld()
			d := w.add(workspace.Workspace{ID: "ws", Path: "/w/ws", Branch: "feat", LinkedPR: 7})
			d.Modified, d.Untracked, d.Ignored, d.Unpushed = c.dir.Modified, c.dir.Untracked, c.dir.Ignored, c.dir.Unpushed
			w.rows = []dinv.PR{{Ref: ref(7), State: "MERGED", HeadBranch: "feat"}}
			if c.book != nil {
				b := turn.Book{Unit: u, Turns: c.book}
				if c.answer != nil {
					b.Answers = []turn.Answer{*c.answer}
				}
				w.books[u] = b
			}
			f := w.facts(t, "ws")
			if got := f["workspace.pendency"]; !slices.Equal(got.([]string), c.want) || got.([]string) == nil {
				t.Errorf("pendency = %#v, want %#v", got, c.want)
			}
		})
	}
}

func TestNewestTurnFacts(t *testing.T) {
	w := newWorld()
	w.add(workspace.Workspace{ID: "ws", Path: "/w/ws", Branch: "feat", LinkedPR: 7})
	w.rows = []dinv.PR{{Ref: ref(7), State: "OPEN", HeadBranch: "feat"}}
	w.books[ref(7).Unit()] = turn.Book{Turns: []turn.Turn{{Seq: 1, State: turn.Started}, {Seq: 2, State: turn.Done}, {Seq: 3, State: turn.Withdrawn}}}
	f := w.facts(t, "ws")
	if f["workspace.newest_turn_state"] != "done" || f["workspace.open_turn"] != true {
		t.Errorf("newest_turn_state = %v, open_turn = %v", f["workspace.newest_turn_state"], f["workspace.open_turn"])
	}
	if f := w.facts(t, "main"); f["workspace.newest_turn_state"] != "" || f["workspace.open_turn"] != false {
		t.Errorf("a workspace without a pull request: %v, %v", f["workspace.newest_turn_state"], f["workspace.open_turn"])
	}
}

func TestATakenOverWorkspaceIsAuthor(t *testing.T) {
	w := newWorld()
	w.add(workspace.Workspace{ID: "taken", Path: "/w/taken", DisplayName: "REVIEW feat", Branch: "feat", LinkedPR: 7})
	w.add(workspace.Workspace{ID: "review", Path: "/w/review", DisplayName: "REVIEW other", Branch: "other", LinkedPR: 8})
	w.add(workspace.Workspace{ID: "mine", Path: "/w/mine", DisplayName: "mine", Branch: "mine"})
	w.add(workspace.Workspace{ID: "spike", Path: "/w/spike", Branch: "spike"})
	w.rows = []dinv.PR{
		{Ref: ref(7), State: "OPEN", HeadBranch: "feat"},
		{Ref: ref(8), State: "OPEN", HeadBranch: "other"},
		{Ref: ref(9), State: "OPEN", HeadBranch: "mine"},
	}
	w.overrides[ref(7).Unit()] = dinv.Override{PR: ref(7), Adopted: true}
	for id, want := range map[string]string{"taken": "author", "review": "review", "mine": "author", "spike": "none"} {
		if got := w.facts(t, id)["workspace.role"]; got != want {
			t.Errorf("%s: role = %v, want %s", id, got, want)
		}
	}
}

func TestTheRoleFollowsThePullRequestsAuthor(t *testing.T) {
	w := newWorld()
	w.viewer = "Me"
	w.add(workspace.Workspace{ID: "colleague", Path: "/w/colleague", DisplayName: "feat/other", Branch: "other", LinkedPR: 7})
	w.add(workspace.Workspace{ID: "mine", Path: "/w/mine", DisplayName: "REVIEW mine", Branch: "mine", LinkedPR: 8})
	w.add(workspace.Workspace{ID: "taken", Path: "/w/taken", DisplayName: "taken", Branch: "taken", LinkedPR: 9})
	w.add(workspace.Workspace{ID: "unknown", Path: "/w/unknown", DisplayName: "REVIEW unknown", Branch: "unknown", LinkedPR: 10})
	w.rows = []dinv.PR{
		{Ref: ref(7), State: "CLOSED", HeadBranch: "other", Author: "colleague"},
		{Ref: ref(8), State: "OPEN", HeadBranch: "mine", Author: "me"},
		{Ref: ref(9), State: "OPEN", HeadBranch: "taken", Author: "colleague"},
		{Ref: ref(10), State: "OPEN", HeadBranch: "unknown"},
	}
	w.overrides[ref(9).Unit()] = dinv.Override{PR: ref(9), Adopted: true}
	for id, want := range map[string]string{"colleague": "review", "mine": "author", "taken": "author", "unknown": "review"} {
		if got := w.facts(t, id)["workspace.role"]; got != want {
			t.Errorf("%s: role = %v, want %s", id, got, want)
		}
	}
	w.viewer = ""
	if got := w.facts(t, "colleague")["workspace.role"]; got != "author" {
		t.Errorf("with no viewer the display name decides: %v", got)
	}
}

func TestAReviewWorkspaceLinksByItsDisplayName(t *testing.T) {
	w := newWorld()
	w.add(workspace.Workspace{ID: "review", Path: "/w/review", DisplayName: "REVIEW feat", Branch: "review-local"})
	w.rows = []dinv.PR{{Ref: ref(7), State: "CLOSED", HeadBranch: "feat", ClosedAt: now.Add(-time.Hour)}}
	f := w.facts(t, "review")
	if f["workspace.pr"] != "acme/api#7" || f["workspace.role"] != "review" || f["workspace.pr_state"] != "closed" {
		t.Errorf("facts = %v", f)
	}
}

func TestTheMainCheckoutOnTheDefaultBranchHasNoPR(t *testing.T) {
	w := newWorld()
	w.rows = []dinv.PR{{Ref: ref(5), State: "OPEN", HeadBranch: "main"}}
	f := w.facts(t, "main")
	if f["workspace.main"] != true || f["workspace.pr"] != "" || f["workspace.repo"] != "acme/api" {
		t.Errorf("facts = %v", f)
	}
}

func TestAGonePathReadsNoGit(t *testing.T) {
	w := newWorld()
	w.add(workspace.Workspace{ID: "gone", Path: "/w/gone", Branch: "feat"})
	delete(w.git.dirs, "/w/gone")
	w.disk.Remove("/w/gone")
	f := w.facts(t, "gone")
	if f["workspace.path_exists"] != false || f["workspace.fingerprint"] != "/w/gone:" {
		t.Errorf("facts = %v", f)
	}
	if _, ok := f["workspace.last_activity"]; ok {
		t.Error("an unknown activity time is a fact")
	}
	if f["workspace.quiet_for"] != time.Duration(0) {
		t.Errorf("quiet_for = %v, want 0 when the activity is unknown", f["workspace.quiet_for"])
	}
}

func TestLastActivityIsTheNewestOfTerminalsPanesAndHead(t *testing.T) {
	w := newWorld()
	w.add(workspace.Workspace{ID: "ws", Path: "/w/ws", Branch: "feat"})
	w.disk.SetTouched("/w/ws", now.Add(-5*time.Hour))
	w.snap.Terminals = []workspace.Terminal{{Handle: "t1", WorkspaceID: "ws", Usable: true, LastOutput: now.Add(-3 * time.Hour)}, {Handle: "t2", WorkspaceID: "ws", LastOutput: now.Add(-time.Hour)}}
	w.snap.Panes = []workspace.Pane{{WorkspaceID: "ws", State: turn.AgentDone, StateSince: now.Add(-2 * time.Hour)}}
	f := w.facts(t, "ws")
	if got := f["workspace.last_activity"]; got != now.Add(-time.Hour) {
		t.Errorf("last_activity = %v", got)
	}
	if f["workspace.sessions"] != 0 {
		t.Errorf("sessions = %v, want none without the registry", f["workspace.sessions"])
	}
}

func TestAnIncompleteSnapshotListsNothing(t *testing.T) {
	w := newWorld()
	w.add(workspace.Workspace{ID: "ws", Path: "/w/ws", Branch: "feat"})
	w.snap.WorkspaceScope.Complete = false
	p := w.provider()
	us, err := p.List(context.Background(), unit.Workspace)
	if err != nil || len(us) != 0 {
		t.Errorf("List = %v, %v; want nothing", us, err)
	}
	_, err = p.Facts(context.Background(), unit.Unit{Provider: unit.Workspace, Key: "/w/absent"})
	if err == nil || errors.Is(err, automation.ErrUnitGone) {
		t.Errorf("Facts of a unit an incomplete listing lacks = %v, want an error that is not ErrUnitGone", err)
	}
}

func TestSeenIsTheGenerationOfTheListing(t *testing.T) {
	w := newWorld()
	p := w.provider()
	if _, err := p.List(context.Background(), unit.Workspace); err != nil {
		t.Fatal(err)
	}
	w.gen = 99
	if p.Seen() != 41 {
		t.Errorf("Seen = %d, want the generation read with the listing", p.Seen())
	}
}

func TestGainIsCachedByFingerprint(t *testing.T) {
	w := newWorld()
	d := w.add(workspace.Workspace{ID: "ws", Path: "/w/ws", Branch: "feat"})
	w.disk.SetSize("/w/ws", 4096)
	p := w.provider()
	ctx := context.Background()
	u := unit.Unit{Provider: unit.Workspace, Key: "/w/ws"}
	for range 2 {
		g, err := p.Gain(ctx, u)
		if err != nil || g.DiskBytes != 4096 {
			t.Fatalf("Gain = %+v, %v", g, err)
		}
	}
	if n, _ := w.disk.Sized(); n != 1 {
		t.Errorf("sized %d times, want once per fingerprint", n)
	}
	d.Head = "moved"
	w.disk.SetSize("/w/ws", 8192)
	g, _ := p.Gain(ctx, u)
	if n, deadline := w.disk.Sized(); g.DiskBytes != 8192 || n != 2 || !deadline {
		t.Errorf("after the head moved: Gain = %+v, sized %d, deadline %v", g, n, deadline)
	}
}

type snapshot struct{ s *workspace.Snapshot }

func (s snapshot) Read(context.Context, workspace.Part) (*workspace.Snapshot, error) {
	cp := *s.s
	return &cp, nil
}

type rows []dinv.PR

func (r rows) Rows(context.Context) ([]dinv.PR, error) { return slices.Clone(r), nil }

func (r rows) Viewer(context.Context) (string, error) { return "", nil }

type viewerRows struct {
	rows
	viewer string
}

func (r viewerRows) Viewer(context.Context) (string, error) { return r.viewer, nil }

type overrides map[unit.Unit]dinv.Override

func (o overrides) Overrides(context.Context) (map[unit.Unit]dinv.Override, error) { return o, nil }

type books map[unit.Unit]turn.Book

func (b books) Books(_ context.Context, us ...unit.Unit) (map[unit.Unit]turn.Book, error) {
	out := map[unit.Unit]turn.Book{}
	for _, u := range us {
		if bk, ok := b[u]; ok {
			out[u] = bk
		}
	}
	return out, nil
}

type gen struct{ w *world }

func (g gen) Gen() port.Generation { return g.w.gen }

type clock struct{ w *world }

func (c clock) Now() time.Time { return c.w.now }

// fakeGit serves workspacestest.Dirs: Modified tracked files, Untracked
// paths, Unpushed commits and Head; Broken dirs fail every read.
type fakeGit struct {
	mu       sync.Mutex
	dirs     map[string]*workspacestest.Dir
	broken   map[string]bool
	statuses int
}

func (g *fakeGit) dir(dir string) (*workspacestest.Dir, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	d, ok := g.dirs[dir]
	if !ok || g.broken[dir] {
		return nil, errors.New("fatal: not a git repository: " + dir)
	}
	return d, nil
}

func (g *fakeGit) Head(_ context.Context, dir string) (string, error) {
	d, err := g.dir(dir)
	if err != nil {
		return "", err
	}
	return d.Head, nil
}

func (g *fakeGit) Status(_ context.Context, dir string) (int, []string, error) {
	d, err := g.dir(dir)
	if err != nil {
		return 0, nil, err
	}
	g.mu.Lock()
	g.statuses++
	g.mu.Unlock()
	return d.Modified, slices.Clone(d.Untracked), nil
}

func (g *fakeGit) Unpushed(_ context.Context, dir string) (int, error) {
	d, err := g.dir(dir)
	if err != nil {
		return 0, err
	}
	return d.Unpushed, nil
}

func (g *fakeGit) reads() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.statuses
}

func TestOSDiskReadsTheWorktreesReflogAndSizes(t *testing.T) {
	root := t.TempDir()
	gd := filepath.Join(root, "main", ".git", "worktrees", "feat")
	ws := filepath.Join(root, "feat")
	for _, d := range []string{filepath.Join(gd, "logs"), ws} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(ws, ".git"), []byte("gitdir: "+gd+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gd, "logs", "HEAD"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	moved := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(gd, "logs", "HEAD"), moved, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	d := factworkspace.OS{}
	if got := d.Touched(ws); !got.Equal(moved) {
		t.Errorf("Touched = %v, want %v", got, moved)
	}
	if !d.Exists(ws) || d.Exists(filepath.Join(root, "absent")) {
		t.Error("Exists")
	}
	if n, err := d.Size(context.Background(), ws); err != nil || n < 100 {
		t.Errorf("Size = %d, %v", n, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.Size(ctx, ws); !errors.Is(err, context.Canceled) {
		t.Errorf("Size after the deadline = %v", err)
	}
}

type recorder struct {
	mu   sync.Mutex
	runs [][]string
}

func (r *recorder) Run(_ context.Context, _ string, _ []string, args ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runs = append(r.runs, slices.Clone(args))
	switch {
	case slices.Contains(args, "rev-list"):
		return []byte("0\n"), nil
	case slices.Contains(args, "rev-parse"):
		return []byte("c0ffee\n"), nil
	}
	return nil, nil
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.runs)
}

func TestFactsRunNoNetworkGit(t *testing.T) {
	w := newWorld()
	w.add(workspace.Workspace{ID: "ws", Path: "/w/ws", Branch: "feat", LinkedPR: 7})
	w.rows = []dinv.PR{{Ref: ref(7), State: "OPEN", HeadBranch: "feat"}}
	rec := &recorder{}
	deps := w.deps()
	deps.Git = factworkspace.LocalGit{R: rec}
	p := factworkspace.New(deps)
	ctx := context.Background()
	us, err := p.List(ctx, unit.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range us {
		if _, err := p.Facts(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	if len(rec.runs) == 0 {
		t.Fatal("no git command ran")
	}
	for _, args := range rec.runs {
		if args[0] != "--no-optional-locks" {
			t.Errorf("git %v may take the index lock", args)
		}
		if slices.Contains(args, "fetch") || slices.Contains(args, "ls-remote") || slices.Contains(args, "--ignored") {
			t.Errorf("git %v reaches the network or walks ignored trees", args)
		}
	}
	if n, _ := w.disk.Sized(); n != 0 {
		t.Error("facts walked a tree")
	}
}

func TestLocalGitStatusLeavesIgnoredOut(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if _, err := (gitx.Exec{}).Run(context.Background(), dir, []string{"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com"}, args...); err != nil {
			t.Fatal(err)
		}
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q", "-b", "main")
	write(".gitignore", "build/\n")
	write("tracked.txt", "a")
	git("add", ".")
	git("commit", "-q", "-m", "init")
	for _, f := range []string{"build/a/1", "build/a/2", "build/b/3"} {
		write(f, "x")
	}
	write("notes.txt", "keep")
	write("tracked.txt", "b")
	g := factworkspace.LocalGit{R: gitx.Exec{}}
	dirty, untracked, err := g.Status(context.Background(), dir)
	if err != nil || dirty != 1 || !slices.Equal(untracked, []string{"notes.txt"}) {
		t.Errorf("Status = %d, %v, %v; want 1 modified and notes.txt", dirty, untracked, err)
	}
	if n, err := g.Unpushed(context.Background(), dir); err != nil || n != 1 {
		t.Errorf("Unpushed = %d, %v; want the commit no remote holds", n, err)
	}
	if head, err := g.Head(context.Background(), dir); err != nil || len(head) != 40 {
		t.Errorf("Head = %q, %v", head, err)
	}
}

func TestAStaleInventoryLinkIsNotThePR(t *testing.T) {
	w := newWorld()
	w.cfg.Cleanup.StaleWorkspace = config.Days(14 * 24 * time.Hour)
	d := w.add(workspace.Workspace{ID: "replant", Path: "/w/replant", Branch: "replant/datastream-runbook"})
	d.Unpushed = 2
	w.disk.SetTouched("/w/replant", now.Add(-20*24*time.Hour))
	w.rows = []dinv.PR{{Ref: ref(5019), State: "CLOSED", Author: "someone", HeadBranch: "develop", ClosedAt: now.Add(-30 * 24 * time.Hour),
		Workspace: dinv.Workspace{ID: "replant"}}}
	f := w.facts(t, "replant")
	if f["workspace.pr"] != "" || f["workspace.role"] != "none" {
		t.Errorf("pr = %v, role = %v; want no pull request", f["workspace.pr"], f["workspace.role"])
	}
	if f["workspace.quiet_for"].(time.Duration) <= time.Duration(w.cfg.Cleanup.StaleWorkspace) || f["workspace.sessions"] != 0 || f["workspace.main"] != false {
		t.Errorf("facts = %v; want it to reach workspace-idle-no-pr", f)
	}
	if !slices.Equal(f["workspace.pendency"].([]string), []string{"commits on no remote"}) {
		t.Errorf("pendency = %v", f["workspace.pendency"])
	}
}

func TestAPrunedGitDirIsAnUnreadableCheckout(t *testing.T) {
	w := newWorld()
	w.add(workspace.Workspace{ID: "ws", Path: "/w/ws", Branch: "feat"})
	w.git.broken = map[string]bool{"/w/ws": true}
	f := w.facts(t, "ws")
	if f["workspace.path_exists"] != true || !slices.Equal(f["workspace.pendency"].([]string), []string{"checkout unreadable"}) {
		t.Errorf("facts = %v", f)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := w.provider().Facts(ctx, unit.Unit{Provider: unit.Workspace, Key: "/w/ws"}); err == nil {
		t.Error("a canceled read reads as an unreadable checkout")
	}
}

func TestLocalFactsAreCachedByFingerprint(t *testing.T) {
	u := unit.Unit{Provider: unit.Workspace, Key: "/w/ws"}
	t.Run("MeteredUnderTheProvidersScope", func(t *testing.T) {
		w := newWorld()
		dir := t.TempDir()
		for name, content := range map[string]string{".git/HEAD": "ref: refs/heads/feat\n", ".git/refs/heads/feat": strings.Repeat("a", 40) + "\n"} {
			if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		w.add(workspace.Workspace{ID: "ws", Path: dir, Branch: "feat"})
		rec := &recorder{}
		deps := w.deps()
		deps.Git = factworkspace.LocalGit{R: gitx.Metered{Next: rec}}
		deps.Disk = factworkspace.OS{}
		p := factworkspace.New(deps)
		reg := meter.NewRegistry(time.Now)
		ctx, sc := meter.Start(meter.WithRegistry(context.Background(), reg), "job:sweep")
		defer sc.End()
		for range 2 {
			if _, err := p.Facts(ctx, w.unit("ws")); err != nil {
				t.Fatal(err)
			}
		}
		if rec.count() != 2 {
			t.Errorf("git ran %d commands over two reads, want status and rev-list once", rec.count())
		}
		var spawns int64
		for _, tot := range reg.Totals() {
			if tot.Name == "job:sweep/"+factworkspace.Scope {
				spawns = tot.Tally.Total(meter.Git).Spawns
			}
		}
		if spawns != 2 {
			t.Errorf("git spawns under job:sweep/%s = %d, want 2", factworkspace.Scope, spawns)
		}
	})
	for name, change := range map[string]func(w *world, d *workspacestest.Dir){
		"head":            func(_ *world, d *workspacestest.Dir) { d.Head = "moved" },
		"index":           func(w *world, _ *workspacestest.Dir) { w.disk.SetIndex("/w/ws", now) },
		"reflog":          func(w *world, _ *workspacestest.Dir) { w.disk.SetTouched("/w/ws", now) },
		"terminal output": func(w *world, _ *workspacestest.Dir) { w.snap.Terminals[0].LastOutput = now },
		"max age":         func(w *world, _ *workspacestest.Dir) { w.now = w.now.Add(time.Duration(w.cfg.Sweep.Slow)) },
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld()
			d := w.add(workspace.Workspace{ID: "ws", Path: "/w/ws", Branch: "feat"})
			w.snap.Terminals = []workspace.Terminal{{Handle: "t1", WorkspaceID: "ws", Usable: true, LastOutput: now.Add(-time.Hour)}}
			p := w.provider()
			read := func() workflow.Facts {
				f, err := p.Facts(context.Background(), u)
				if err != nil {
					t.Fatal(err)
				}
				return f
			}
			read()
			d.Modified = 3
			if read()["workspace.dirty"] != 0 || w.git.reads() != 1 {
				t.Fatalf("an unchanged fingerprint read git again (%d reads)", w.git.reads())
			}
			change(w, d)
			if f := read(); f["workspace.dirty"] != 3 || w.git.reads() != 2 {
				t.Errorf("after the %s changed: dirty = %v, %d reads", name, f["workspace.dirty"], w.git.reads())
			}
		})
	}
}

func TestTheIndexIsBuiltOncePerGeneration(t *testing.T) {
	w := newWorld()
	w.add(workspace.Workspace{ID: "a", Path: "/w/a", Branch: "a"})
	w.add(workspace.Workspace{ID: "b", Path: "/w/b", Branch: "b"})
	n := 0
	deps := w.deps()
	deps.PRs = countingRows{rows(w.rows), &n}
	p := factworkspace.New(deps)
	for _, id := range []string{"a", "b", "main"} {
		if _, err := p.Facts(context.Background(), w.unit(id)); err != nil {
			t.Fatal(err)
		}
	}
	w.gen++
	if _, err := p.Facts(context.Background(), unit.Unit{Provider: unit.Workspace, Key: "/w/a"}); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("rows read %d times, want once per change-log generation", n)
	}
}

type countingRows struct {
	rows
	n *int
}

func (c countingRows) Rows(ctx context.Context) ([]dinv.PR, error) {
	*c.n++
	return c.rows.Rows(ctx)
}

func TestACheckoutWithoutGitIsUnreadableWithoutRunningGit(t *testing.T) {
	w := newWorld()
	w.add(workspace.Workspace{ID: "ws", Path: "/w/main/.claude/worktrees/x", Branch: "x"})
	w.disk.Remove("/w/main/.claude/worktrees/x/.git")
	rec := &recorder{}
	deps := w.deps()
	deps.Git = factworkspace.LocalGit{R: rec}
	f, err := factworkspace.New(deps).Facts(context.Background(), w.unit("ws"))
	if err != nil {
		t.Fatal(err)
	}
	if f["workspace.path_exists"] != true || !slices.Equal(f["workspace.pendency"].([]string), []string{"checkout unreadable"}) {
		t.Errorf("facts = %v", f)
	}
	if rec.count() != 0 {
		t.Errorf("git ran %v in a directory that is no checkout: it reads the enclosing repository", rec.runs)
	}
}

func TestASiblingSharesTheReadsAndKeepsItsOwnSeen(t *testing.T) {
	w := newWorld()
	w.add(workspace.Workspace{ID: "ws", Path: "/w/ws", Branch: "feat"})
	w.disk.SetSize("/w/ws", 4096)
	p := w.provider()
	s := p.Sibling()
	ctx := context.Background()
	u := unit.Unit{Provider: unit.Workspace, Key: "/w/ws"}
	read := func(q *factworkspace.Provider) {
		t.Helper()
		if _, err := q.List(ctx, unit.Workspace); err != nil {
			t.Fatal(err)
		}
		if _, err := q.Facts(ctx, u); err != nil {
			t.Fatal(err)
		}
		if _, err := q.Gain(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	read(p)
	w.gen = 77
	read(s)
	if n := w.git.reads(); n != 1 {
		t.Errorf("git status ran %d times, want once for both", n)
	}
	if n, _ := w.disk.Sized(); n != 1 {
		t.Errorf("sized %d times, want once for both", n)
	}
	if p.Seen() != 41 || s.Seen() != 77 {
		t.Errorf("Seen = %d and %d, want 41 and 77", p.Seen(), s.Seen())
	}
}

func TestTheOwnerIsTheWorkspacesPR(t *testing.T) {
	w := newWorld()
	w.add(workspace.Workspace{ID: "review", Path: "/w/review", DisplayName: "REVIEW feat", Branch: "review-local"})
	w.rows = []dinv.PR{{Ref: ref(7), State: "CLOSED", HeadBranch: "feat", ClosedAt: now.Add(-time.Hour)}}
	p := w.provider()
	ctx := context.Background()
	if got, err := p.Owners(ctx, w.unit("review")); err != nil || !slices.Equal(got, []unit.Unit{ref(7).Unit()}) {
		t.Errorf("Owners(review) = %v, %v; want %s", got, err, ref(7).Unit())
	}
	if got, err := p.Owners(ctx, w.unit("main")); err != nil || len(got) != 0 {
		t.Errorf("Owners of the main checkout = %v, %v; want none", got, err)
	}
}
