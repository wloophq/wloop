package cli

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	factsgithub "github.com/wloophq/wloop/internal/adapter/facts/github"
	factsession "github.com/wloophq/wloop/internal/adapter/facts/session"
	factworkspace "github.com/wloophq/wloop/internal/adapter/facts/workspace"
	"github.com/wloophq/wloop/internal/adapter/facts/workspace/factworkspacetest"
	"github.com/wloophq/wloop/internal/adapter/gitcheck"
	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/automation/automationtest"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/gitx"
	"github.com/wloophq/wloop/internal/source"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

const cleanupFixture = "testdata/cleanup"

// clockA is the clock the fixture was written against.
var clockA = time.Date(2026, 10, 20, 18, 20, 0, 0, time.UTC)

type fxRepo struct {
	ID            string   `json:"id"`
	Path          string   `json:"path"`
	RemoteKey     string   `json:"remote_key"`
	DefaultBranch string   `json:"default_branch"`
	Gitignore     []string `json:"gitignore"`
}

type fxIgnored struct {
	Path       string `json:"path"`
	SameAsMain bool   `json:"same_as_main"`
}

type fxCheckout struct {
	Detached     bool        `json:"detached"`
	Pushed       bool        `json:"pushed"`
	RemoteBranch bool        `json:"remote_branch"`
	PRHead       bool        `json:"pr_head"`
	Unpushed     int         `json:"unpushed"`
	Modified     []string    `json:"modified"`
	Untracked    []string    `json:"untracked"`
	Ignored      []fxIgnored `json:"ignored"`
}

type fxWorkspace struct {
	ID          string      `json:"id"`
	RepoID      string      `json:"repo_id"`
	Path        string      `json:"path"`
	DisplayName string      `json:"display_name"`
	Branch      string      `json:"branch"`
	LinkedPR    int         `json:"linked_pr"`
	Main        bool        `json:"main"`
	Host        string      `json:"host"`
	Scope       string      `json:"scope"`
	Touched     string      `json:"touched"`
	Checkout    *fxCheckout `json:"checkout"`
}

type fxTerminal struct {
	Handle      string `json:"handle"`
	WorkspaceID string `json:"workspace_id"`
	TabID       string `json:"tab_id"`
	Agent       string `json:"agent"`
	Usable      bool   `json:"usable"`
	LastOutput  string `json:"last_output"`
	AgentState  string `json:"agent_state"`
}

type fxSession struct {
	Handle       string `json:"handle"`
	Pane         string `json:"pane"`
	AgentSession string `json:"agent_session"`
	WorkspaceID  string `json:"workspace_id"`
	Worktree     string `json:"worktree"`
	Managed      bool   `json:"managed"`
	StartedAt    string `json:"started_at"`
	MCPAt        string `json:"mcp_at"`
	IdleSince    string `json:"idle_since"`
	WorktreeGone bool   `json:"worktree_gone"`
}

type fxTurn struct {
	Seq          int64
	Intent       string
	Round        string
	Trigger      string
	SentTo       string
	Session      string
	State        string
	SentAt       string
	StartedAt    string
	EndedAt      string
	Agent        string
	AgentSession string
}

type fxRecord struct {
	TurnSeq int64  `json:"turn_seq"`
	Handle  string `json:"handle"`
	At      string `json:"at"`
	State   string `json:"state"`
}

type fxAnswer struct {
	TurnSeq int64  `json:"turn_seq"`
	Route   string `json:"route"`
	At      string `json:"at"`
}

type fxBook struct {
	Unit    string     `json:"unit"`
	Turns   []fxTurn   `json:"turns"`
	Records []fxRecord `json:"records"`
	Answers []fxAnswer `json:"answers"`
}

type fxPR struct {
	Ref        string `json:"ref"`
	State      string `json:"state"`
	MergedAt   string `json:"merged_at"`
	ClosedAt   string `json:"closed_at"`
	HeadBranch string `json:"head_branch"`
	Head       string `json:"head"`
	Author     string `json:"author"`
}

type cleanupRow struct {
	Unit      string `json:"unit"`
	Clock     string `json:"clock"`
	Workflow  string `json:"workflow"`
	Decision  string `json:"decision"`
	Reason    string `json:"reason,omitempty"`
	Threshold string `json:"threshold,omitempty"`
}

type cleanupExpected struct {
	ClockA string       `json:"clock_a"`
	ClockB string       `json:"clock_b"`
	Rows   []cleanupRow `json:"rows"`
}

func fxTime(t *testing.T, s string) time.Time {
	t.Helper()
	if s == "" || strings.HasPrefix(s, "0001-") {
		return time.Time{}
	}
	at, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("time %q: %v", s, err)
	}
	return at
}

func readFixture(t *testing.T, dir, name string, v any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

// fixturePaths puts the fixture's paths under a test directory: the facts
// and the removal read the fixture's paths, git and the disk the real ones.
type fixturePaths struct{ roots []string }

var fixtureRoots = []string{"/Users/user/", "/private/tmp/"}

func (p fixturePaths) real(s string) string {
	for _, r := range fixtureRoots {
		if strings.HasPrefix(s, r) {
			return filepath.Join(p.roots[0], s)
		}
	}
	return s
}

func (p fixturePaths) fixture(s string) string {
	for _, root := range p.roots {
		s = strings.ReplaceAll(s, root+"/", "/")
	}
	return s
}

// fixtureGit runs git on the real checkouts of the fixture's paths. It keeps
// the answers of reading commands until a writing one runs or a test changes
// a file (forget): every spawn costs, and the checkouts are shared.
type fixtureGit struct {
	p    fixturePaths
	next gitx.Runner
}

var gitAnswers = struct {
	mu   sync.Mutex
	seen map[string]gitAnswer
}{seen: map[string]gitAnswer{}}

type gitAnswer struct {
	out []byte
	err error
}

var readingGit = []string{"rev-parse", "status", "rev-list", "symbolic-ref", "show-ref", "for-each-ref", "merge-base", "ls-remote", "log", "cat-file", "stash", "config"}

func forgetGit() {
	gitAnswers.mu.Lock()
	clear(gitAnswers.seen)
	gitAnswers.mu.Unlock()
}

func (g fixtureGit) Run(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
	mapped := make([]string, len(args))
	for i, a := range args {
		mapped[i] = g.p.real(a)
	}
	verb := ""
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			verb = a
			break
		}
	}
	key := dir + "\x00" + strings.Join(args, "\x00")
	reading := slices.Contains(readingGit, verb) && !(verb == "stash" && !slices.Contains(args, "list"))
	if reading {
		gitAnswers.mu.Lock()
		a, ok := gitAnswers.seen[key]
		gitAnswers.mu.Unlock()
		if ok {
			return a.out, a.err
		}
	} else {
		forgetGit()
	}
	out, err := g.next.Run(ctx, g.p.real(dir), env, mapped...)
	out = []byte(g.p.fixture(string(out)))
	if reading && ctx.Err() == nil {
		gitAnswers.mu.Lock()
		gitAnswers.seen[key] = gitAnswer{out, err}
		gitAnswers.mu.Unlock()
	}
	return out, err
}

type fixtureFiles struct {
	p    fixturePaths
	next gitcheck.Files
}

func (f fixtureFiles) Same(a, b string) (bool, error)     { return f.next.Same(f.p.real(a), f.p.real(b)) }
func (f fixtureFiles) Exists(path string) bool            { return f.next.Exists(f.p.real(path)) }
func (f fixtureFiles) Link(path string) bool              { return f.next.Link(f.p.real(path)) }
func (f fixtureFiles) Repos(dir string) ([]string, error) { return f.next.Repos(f.p.real(dir)) }

// fixtureSnapshot is the manager's listing: the fake manager's workspaces as
// they are now and the terminals the sessions fake still holds.
type fixtureSnapshot struct {
	mu       sync.Mutex
	gen      uint64
	seen     string
	repos    []wsdomain.Repo
	mgr      *fake.Manager
	terms    []wsdomain.Terminal
	panes    []wsdomain.Pane
	sessions *turnstest.Sessions
}

func (s *fixtureSnapshot) Read(ctx context.Context, _ wsdomain.Part) (*wsdomain.Snapshot, error) {
	list, err := s.mgr.List(ctx)
	if err != nil {
		return nil, err
	}
	repos := slices.Clone(s.repos)
	for i := range list {
		list[i].Manager = s.mgr.Name()
	}
	for i := range repos {
		repos[i].Manager = s.mgr.Name()
	}
	census, err := s.sessions.Census(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var terms []wsdomain.Terminal
	for _, t := range s.terms {
		if _, live := census.Sessions[t.Handle]; live {
			terms = append(terms, t)
		}
	}
	if seen := fmt.Sprint(list, terms); seen != s.seen {
		s.gen, s.seen = s.gen+1, seen
	}
	scope := wsdomain.Scope{Hosts: []string{wsdomain.LocalHost}, Complete: true, RuntimeID: "fixture"}
	return &wsdomain.Snapshot{Gen: s.gen, Repos: repos, Workspaces: list, Panes: slices.Clone(s.panes), Terminals: terms,
		WorkspaceScope: scope, TerminalScope: scope, Loaded: wsdomain.PartAll}, nil
}

// fixtureEnv is the fixture's machine as the engine sees it.
type fixtureEnv struct {
	te         *testEnv
	db         *sqlite.DB
	paths      fixturePaths
	mgr        *fake.Manager
	sessions   *turnstest.Sessions
	snap       *fixtureSnapshot
	disk       *factworkspacetest.Disk
	workspaces []fxWorkspace
	exists     map[string]bool
	now        time.Time
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(gitx.Binary(), append([]string{"-C", dir, "-c", "user.name=fixture", "-c", "user.email=fixture@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("git -C %s %s: %v: %s", dir, strings.Join(args, " "), err, errb.String())
	}
	return strings.TrimSpace(out.String())
}

func commitFile(t *testing.T, dir, file, content string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, file), content)
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "change "+file)
}

func originOf(paths fixturePaths, r fxRepo) string {
	return filepath.Join(paths.roots[0], "origin", strings.ReplaceAll(r.RemoteKey, "/", "_")+".git")
}

// buildCheckouts makes each repository's origin and main checkout, then each
// worktree as its checkout spec says; it returns the head each pull
// request's remote ref got.
func buildCheckouts(t *testing.T, paths fixturePaths, repos []fxRepo, wss []fxWorkspace, prs []fxPR) map[string]string {
	t.Helper()
	headBranch := map[string]string{}
	for _, p := range prs {
		headBranch[p.Ref] = p.HeadBranch
	}
	repoOf := map[string]fxRepo{}
	for _, r := range repos {
		repoOf[r.ID] = r
		gitRun(t, "/", "init", "-q", "--bare", "-b", r.DefaultBranch, originOf(paths, r))
		main := paths.real(r.Path)
		gitRun(t, "/", "init", "-q", "-b", r.DefaultBranch, main)
		ignore := ""
		for _, g := range r.Gitignore {
			ignore += g + "\n"
		}
		writeFile(t, filepath.Join(main, ".gitignore"), ignore)
		commitFile(t, main, "README.md", r.RemoteKey+"\n")
		gitRun(t, main, "remote", "add", "origin", originOf(paths, r))
		gitRun(t, main, "push", "-q", "-u", "origin", r.DefaultBranch)
	}
	heads := map[string]string{}
	pushed := map[string]string{}
	for _, w := range wss {
		r, c := repoOf[w.RepoID], w.Checkout
		main, dir := paths.real(r.Path), paths.real(w.Path)
		owner, name := ownerName(r.RemoteKey)
		ref := ""
		if n := prNumberOf(w, prs, owner, name); n != 0 {
			ref = fmt.Sprintf("%s/%s#%d", owner, name, n)
		}
		switch {
		case w.Main && w.Branch != r.DefaultBranch:
			gitRun(t, main, "checkout", "-q", "-b", w.Branch)
		case w.Main:
		case c.Detached:
			gitRun(t, main, "worktree", "add", "-q", "--detach", dir, r.DefaultBranch)
		default:
			gitRun(t, main, "worktree", "add", "-q", "-b", w.Branch, dir, r.DefaultBranch)
		}
		modified := slices.Clone(c.Modified)
		addModified := func() {
			for _, f := range modified {
				writeFile(t, filepath.Join(dir, f), "committed\n")
			}
			modified = nil
		}
		if c.Pushed && !c.Detached {
			remote := cmp.Or(headBranch[ref], w.Branch)
			if oid, ok := pushed[r.ID+":"+remote]; ok {
				gitRun(t, dir, "fetch", "-q", "origin")
				gitRun(t, dir, "reset", "-q", "--hard", oid)
			} else if !w.Main || w.Branch != r.DefaultBranch {
				addModified()
				commitFile(t, dir, "work-1.txt", w.Path+"\n")
				commitFile(t, dir, "work-2.txt", w.Path+"\n")
				gitRun(t, dir, "push", "-q", "origin", "HEAD:refs/heads/"+remote)
				pushed[r.ID+":"+remote] = gitRun(t, dir, "rev-parse", "HEAD")
			}
			gitRun(t, dir, "fetch", "-q", "origin")
			if remote == w.Branch {
				gitRun(t, dir, "branch", "-q", "--set-upstream-to", "origin/"+remote)
			}
			if c.PRHead && ref != "" {
				gitRun(t, dir, "push", "-q", "origin", "HEAD:refs/pull/"+strings.Split(ref, "#")[1]+"/head")
				heads[ref] = gitRun(t, dir, "rev-parse", "HEAD")
			}
			if !c.RemoteBranch {
				gitRun(t, "/", "--git-dir", originOf(paths, r), "update-ref", "-d", "refs/heads/"+remote)
			}
		}
		for i := range c.Unpushed {
			if i == 0 {
				addModified()
			}
			commitFile(t, dir, fmt.Sprintf("local-%d.txt", i+1), w.Path+"\n")
		}
		for _, f := range c.Modified {
			writeFile(t, filepath.Join(dir, f), "changed by hand\n")
		}
		for _, f := range c.Untracked {
			writeFile(t, filepath.Join(dir, f), "notes\n")
		}
		for _, ig := range c.Ignored {
			content := "secret\n"
			if !ig.SameAsMain {
				content = "another secret\n"
			}
			writeFile(t, filepath.Join(dir, ig.Path), content)
			writeFile(t, filepath.Join(main, ig.Path), "secret\n")
		}
	}
	return heads
}

// checkouts are built once per test binary: git is most of a fixture's
// cost. Tests restore any file they change.
var fxCheckouts struct {
	once  sync.Once
	ok    bool
	paths fixturePaths
	heads map[string]string
}

func TestMain(m *testing.M) {
	os.Unsetenv("ORCA_USER_DATA_PATH")
	code := m.Run()
	if len(fxCheckouts.paths.roots) > 0 {
		os.RemoveAll(fxCheckouts.paths.roots[0])
	}
	os.Exit(code)
}

func sharedCheckouts(t *testing.T, repos []fxRepo, wss []fxWorkspace, prs []fxPR) (fixturePaths, map[string]string) {
	t.Helper()
	fxCheckouts.once.Do(func() {
		root, err := os.MkdirTemp("", "cleanup-fixture-")
		if err != nil {
			t.Fatal(err)
		}
		fxCheckouts.paths.roots = []string{root}
		if resolved, err := filepath.EvalSymlinks(root); err == nil && resolved != root {
			fxCheckouts.paths.roots = append(fxCheckouts.paths.roots, resolved)
		}
		fxCheckouts.heads = buildCheckouts(t, fxCheckouts.paths, repos, wss, prs)
		fxCheckouts.ok = true
	})
	if !fxCheckouts.ok {
		t.Fatal("the fixture's checkouts failed to build")
	}
	return fxCheckouts.paths, fxCheckouts.heads
}

func ownerName(remoteKey string) (string, string) {
	parts := strings.Split(remoteKey, "/")
	return parts[1], parts[2]
}

// prNumberOf is the pull request a workspace's checkout stands for: its
// link, else the stored pull request on its branch.
func prNumberOf(w fxWorkspace, prs []fxPR, owner, name string) int {
	if w.LinkedPR != 0 {
		return w.LinkedPR
	}
	for _, p := range prs {
		ref, err := pr.ParseKey(p.Ref)
		if err == nil && ref.Owner == owner && ref.Repo == name && p.HeadBranch == w.Branch && p.Head == "worktree" {
			return ref.Number
		}
	}
	return 0
}

// loadFixture builds the fixture of dir at now: the fake manager and its
// snapshot, the sessions fake, a store seeded with the registry, the books
// and the pull requests, and the checkouts under a test directory.
func loadFixture(t *testing.T, dir string, now time.Time) *fixtureEnv {
	t.Helper()
	ctx := context.Background()
	var wsf struct {
		Repos      []fxRepo      `json:"repos"`
		Workspaces []fxWorkspace `json:"workspaces"`
	}
	var terms []fxTerminal
	var sess []fxSession
	var books []fxBook
	var prf struct {
		Viewer string `json:"viewer"`
		PRs    []fxPR `json:"prs"`
	}
	readFixture(t, dir, "workspaces.json", &wsf)
	readFixture(t, dir, "terminals.json", &terms)
	readFixture(t, dir, "sessions.json", &sess)
	readFixture(t, dir, "books.json", &books)
	readFixture(t, dir, "prs.json", &prf)

	paths, heads := sharedCheckouts(t, wsf.Repos, wsf.Workspaces, prf.PRs)
	f := &fixtureEnv{paths: paths, workspaces: wsf.Workspaces, exists: map[string]bool{}, now: now}

	f.mgr = &fake.Manager{}
	var repos []wsdomain.Repo
	for _, r := range wsf.Repos {
		repos = append(repos, wsdomain.Repo{ID: r.ID, Path: r.Path, RemoteKey: r.RemoteKey, DefaultBranch: r.DefaultBranch})
	}
	f.mgr.RepoList = repos
	f.disk = factworkspacetest.NewDisk()
	for _, w := range wsf.Workspaces {
		f.mgr.WorkspaceList = append(f.mgr.WorkspaceList, wsdomain.Workspace{ID: w.ID, RepoID: w.RepoID, Path: w.Path, DisplayName: w.DisplayName,
			Branch: w.Branch, HostID: w.Host, LinkedPR: w.LinkedPR, IsMain: w.Main})
		f.disk.Put(w.Path, fxTime(t, w.Touched))
		f.disk.Put(filepath.Join(w.Path, ".git"), time.Time{})
		f.exists[w.Path] = true
	}

	te := newTestEnv(t, f.mgr)
	f.te = te
	te.Env.Now = func() time.Time { return f.now }
	te.Env.Git = gitx.Git{R: fixtureGit{p: f.paths, next: gitx.Exec{}}}
	te.Env.Files = fixtureFiles{p: f.paths}
	te.changes = change.NewLog(change.DefaultWindow, nil)
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), storeFile), te.changes)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	f.db = db
	te.Rounds = sqlite.NewRounds(db)
	te.Store = inventory.New(db)
	te.repo = sqlite.New(db)
	f.sessions = turnstest.NewSessions()
	prompts := turnstest.NewPrompts()
	te.Turns.Store, te.Turns.Registry, te.Turns.Prompts = te.repo, te.repo, prompts
	te.Turns.Sessions = turns.NewRouter(te.repo, handleSessions{f.mgr.Name(), f.sessions})
	te.Turns.GitHub = inventoryFacts{te.Store}
	te.Views.Books, te.Views.State = te.repo, te.repo

	if err := te.Store.SetViewer(ctx, unit.GitHub, prf.Viewer); err != nil {
		t.Fatal(err)
	}
	for _, p := range prf.PRs {
		ref, err := pr.ParseKey(p.Ref)
		if err != nil {
			t.Fatal(err)
		}
		row := dinv.PR{Ref: ref, State: p.State, Author: p.Author, HeadBranch: p.HeadBranch, HeadOid: heads[p.Ref], MergedAt: fxTime(t, p.MergedAt), ClosedAt: fxTime(t, p.ClosedAt),
			Reasons: []string{"review-requested"}, LastSeenAt: now}
		if err := te.Store.UpsertPR(ctx, row); err != nil {
			t.Fatal(err)
		}
	}

	panes := map[string]string{}
	for _, s := range sess {
		panes[s.Handle] = s.Pane
		if s.Worktree != "" && !s.WorktreeGone {
			f.exists[s.Worktree] = true
		}
		row := turn.Session{ID: s.Handle, Manager: f.mgr.Name(), Handle: s.Handle, Pane: s.Pane, AgentSession: s.AgentSession, WorkspaceID: s.WorkspaceID, Worktree: s.Worktree,
			Managed: s.Managed, StartedAt: fxTime(t, s.StartedAt), MCPAt: fxTime(t, s.MCPAt), IdleSince: fxTime(t, s.IdleSince)}
		if err := te.repo.SaveSession(ctx, row); err != nil {
			t.Fatal(err)
		}
		if err := te.repo.BindHandle(ctx, row.ID, row.Manager, row.Handle, "", now); err != nil {
			t.Fatal(err)
		}
		prompts.Put(s.Handle, row.StartedAt)
	}
	f.snap = &fixtureSnapshot{repos: repos, mgr: f.mgr, sessions: f.sessions}
	byWorkspace := map[string][]turns.Terminal{}
	var agents []turns.AgentObservation
	for _, tm := range terms {
		last := fxTime(t, tm.LastOutput)
		f.snap.terms = append(f.snap.terms, wsdomain.Terminal{Manager: f.mgr.Name(), Handle: tm.Handle, WorkspaceID: tm.WorkspaceID, TabID: tm.TabID, Agent: tm.Agent, LastOutput: last, Usable: tm.Usable})
		byWorkspace[tm.WorkspaceID] = append(byWorkspace[tm.WorkspaceID], turns.Terminal{Session: tm.Handle, Key: tm.TabID, Agent: tm.Agent, LastOutput: last})
		if _, path, ok := strings.Cut(tm.WorkspaceID, "::"); ok {
			f.exists[path] = true
		}
		if tm.AgentState == "" {
			continue
		}
		state := turn.ParseAgentState(tm.AgentState)
		agents = append(agents, turns.AgentObservation{WorkspaceID: tm.WorkspaceID, Pane: panes[tm.Handle], TerminalKey: tm.TabID, State: state, StateSince: last})
		f.snap.panes = append(f.snap.panes, wsdomain.Pane{Manager: f.mgr.Name(), WorkspaceID: tm.WorkspaceID, Pane: cmp.Or(panes[tm.Handle], tm.TabID), State: state, StateSince: last})
	}
	for id, ts := range byWorkspace {
		f.sessions.Put(id, ts...)
	}
	f.sessions.SetAgents(agents...)

	for _, b := range books {
		f.saveBook(t, b)
	}
	fetches := db.Fetches()
	for _, w := range wsf.Workspaces {
		if err := fetches.Mark(ctx, w.ID, now); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *fixtureEnv) saveBook(t *testing.T, fb fxBook) {
	t.Helper()
	ctx := context.Background()
	ref, err := pr.ParseKey(fb.Unit)
	if err != nil {
		t.Fatal(err)
	}
	b := turn.Book{Unit: ref.Unit()}
	for _, x := range fb.Turns {
		b.Turns = append(b.Turns, turn.Turn{Intent: turn.Intent(x.Intent), ID: x.Round, Trigger: turn.Trigger(x.Trigger), SentTo: x.SentTo, Session: x.Session,
			State: turn.State(x.State), SentAt: fxTime(t, x.SentAt), StartedAt: fxTime(t, x.StartedAt), EndedAt: fxTime(t, x.EndedAt), Agent: x.Agent, AgentSession: x.AgentSession})
	}
	saved, err := f.te.repo.Save(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	seq := map[int64]int64{}
	for i, x := range fb.Turns {
		seq[x.Seq] = saved.Turns[i].Seq
	}
	more := turn.Book{Unit: b.Unit}
	for i, r := range fb.Records {
		more.Records = append(more.Records, turn.Record{TurnSeq: seq[r.TurnSeq], Session: r.Handle, At: fxTime(t, r.At), Acked: true,
			Hash: fmt.Sprintf("fixture-%d", i), Record: record.Record{State: record.State(r.State)}})
	}
	for _, a := range fb.Answers {
		more.Answers = append(more.Answers, turn.Answer{TurnSeq: seq[a.TurnSeq], Route: turn.Route(a.Route), Source: turn.Cockpit, At: fxTime(t, a.At)})
	}
	if len(more.Records)+len(more.Answers) > 0 {
		if _, err := f.te.repo.Save(ctx, more); err != nil {
			t.Fatal(err)
		}
	}
}

// engine is the daemon's engine over the fixture: the bundled workflows with
// the test config, the facts providers over the fixture's snapshot and
// checkouts, the builtin steps.
func (f *fixtureEnv) engine(t *testing.T) *automation.Engine {
	t.Helper()
	te := f.te
	clock := clockFunc(func() time.Time { return f.now })
	sf := factsession.New(factsession.Deps{Turns: te.Turns, Snapshot: f.snap, PRs: inventory.ByUnit{Store: te.Store},
		Exists: func(p string) bool { return f.exists[p] }, Changes: te.changes, Cfg: te.currentConfig, Clock: clock})
	wf := factworkspace.New(factworkspace.Deps{Snapshot: f.snap, PRs: factsgithub.InventoryInputs(te.Store), Overrides: inventory.ByUnit{Store: te.Store},
		Books: te.repo, Git: factworkspace.LocalGit{R: te.Env.Git.R}, Disk: f.disk, Changes: te.changes, Cfg: te.currentConfig, Clock: clock})
	gh := factsgithub.New(factsgithub.Deps{Inputs: factsgithub.InventoryInputs(te.Store), Store: te.Store, Snapshot: f.snap, Books: te.repo,
		Rounds: sqlite.NewRounds(f.db), Git: factsgithub.LocalGit(te.Env.Git), Branches: te.Env.Git, Cfg: te.currentConfig, Clock: clock, Changes: te.changes,
		Teams: func(context.Context) ([]string, error) { return nil, nil }, Intents: func() *dintent.Registry { return te.loadedIntents().Registry }})
	return te.newEngine(engineDeps{DB: f.db, Modules: []source.Module{{ID: unit.GitHub, Codec: pr.Codec{}, Facts: gh}}, SessionFacts: sf, WorkspaceFacts: wf, Snapshot: f.snap})
}

func (f *fixtureEnv) setMode(name string, m workflow.Mode) {
	if f.te.cfg.Workflows == nil {
		f.te.cfg.Workflows = map[string]config.WorkflowConfig{}
	}
	wc := f.te.cfg.Workflows[name]
	wc.Mode = string(m)
	f.te.cfg.Workflows[name] = wc
}

func (f *fixtureEnv) workspace(t *testing.T, suffix string) fxWorkspace {
	t.Helper()
	for _, w := range f.workspaces {
		if strings.HasSuffix(w.Path, "/"+suffix) {
			return w
		}
	}
	t.Fatalf("no workspace %s", suffix)
	return fxWorkspace{}
}

func (f *fixtureEnv) removed() []string {
	var out []string
	for _, w := range f.mgr.Writes {
		if id, ok := strings.CutPrefix(w, "remove "); ok {
			out = append(out, id)
		}
	}
	return out
}

// short is d to the minute without the trailing zero units: 26h10m, 2h, 45m.
func short(d time.Duration) string {
	s := d.Round(time.Minute).String()
	s = strings.TrimSuffix(s, "0s")
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

func compare(d, limit time.Duration) string {
	switch {
	case d > limit:
		return ">"
	case d == limit:
		return "="
	}
	return "<"
}

// threshold names what keeps a unit with no item from the workflow the
// manual cleanup corresponds to.
func threshold(f workflow.Facts, c config.Cleanup) (string, string) {
	dur := func(name string) time.Duration { d, _ := f[name].(time.Duration); return d }
	str := func(name string) string { s, _ := f[name].(string); return s }
	idle := func(limitName string, limit time.Duration) string {
		if _, ok := f["session.idle_since"]; !ok {
			return "session.idle_since unknown: never seen idle"
		}
		return fmt.Sprintf("session.idle_for %s %s %s %s", short(dur("session.idle_for")), compare(dur("session.idle_for"), limit), limitName, short(limit))
	}
	switch {
	case f["session.fingerprint"] != nil:
		if managed, _ := f["session.managed"].(bool); !managed {
			if a := str("session.agent_state"); a != "done" {
				return "session-manual-quiet", fmt.Sprintf("session.agent_state %q is not done", a)
			}
			return "session-manual-quiet", idle("cleanup.manual_idle", time.Duration(c.ManualIdle))
		}
		switch s := str("session.newest_state"); s {
		case "done", "settled", "failed":
			return "session-finished-quiet", idle("cleanup.idle", time.Duration(c.Idle))
		case "asked":
			return "session-asked-quiet", idle("cleanup.asked_idle", time.Duration(c.AskedIdle))
		case "":
			return "session-finished-quiet", "session.newest_state empty: no turn on it"
		default:
			return "session-finished-quiet", fmt.Sprintf("session.newest_state %q", s)
		}
	case f["workspace.fingerprint"] != nil:
		if main, _ := f["workspace.main"].(bool); main {
			return "-", "workspace.main"
		}
		role, state := str("workspace.role"), str("workspace.pr_state")
		name := "workspace-" + role + "-finished"
		switch {
		case str("workspace.pr") == "":
			if n, _ := f["workspace.sessions"].(int); n > 0 {
				return "workspace-idle-no-pr", fmt.Sprintf("workspace.sessions %d", n)
			}
			stale := time.Duration(c.StaleWorkspace)
			return "workspace-idle-no-pr", fmt.Sprintf("workspace.quiet_for %s %s cleanup.stale_workspace %s", short(dur("workspace.quiet_for")),
				compare(dur("workspace.quiet_for"), stale), short(stale))
		case state == "merged" || state == "closed":
			grace := time.Duration(c.WorkspaceGrace)
			return name, fmt.Sprintf("workspace.pr_finished_for %s %s cleanup.workspace_grace %s", short(dur("workspace.pr_finished_for")),
				compare(dur("workspace.pr_finished_for"), grace), short(grace))
		default:
			return name, fmt.Sprintf("workspace.pr_state %q", state)
		}
	}
	id, _ := f["pr.orphan_answer_id"].(int)
	return "hygiene-turn-on-finished-pr", fmt.Sprintf("pr.open_turn %q, pr.state %q, pr.orphan_answer_id %d", str("pr.open_turn"), str("pr.state"), id)
}

// rows are the plan of every unit at f.now: one row per item, and a not
// matched row naming the threshold for a unit with none.
// rows plans the fixture once its agent processes no session took became
// sessions, as a sweep's refresh takes them.
func (f *fixtureEnv) rows(t *testing.T, eng *automation.Engine, clock string) []cleanupRow {
	t.Helper()
	ctx := context.Background()
	if err := f.te.Turns.KeepSessions(ctx); err != nil {
		t.Fatal(err)
	}
	plan, err := eng.Evaluate(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Errors) > 0 {
		t.Fatalf("plan errors: %v", plan.Errors)
	}
	byUnit := map[unit.Unit][]workflow.Item{}
	for _, it := range plan.Items {
		byUnit[it.Unit] = append(byUnit[it.Unit], it)
	}
	cfg := f.te.currentConfig().Cleanup
	var out []cleanupRow
	for _, p := range []unit.ProviderID{unit.Session, unit.Workspace, unit.GitHub} {
		units, err := eng.Units.List(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		for _, u := range units {
			items := byUnit[u]
			if len(items) == 0 {
				facts, err := eng.Units.Facts(ctx, u)
				if err != nil {
					t.Fatal(err)
				}
				name, why := threshold(facts, cfg)
				out = append(out, cleanupRow{Unit: u.String(), Clock: clock, Workflow: name, Decision: "not matched", Threshold: why})
				continue
			}
			for _, it := range items {
				out = append(out, cleanupRow{Unit: u.String(), Clock: clock, Workflow: it.Workflow, Decision: string(it.Decision), Reason: it.Reason})
			}
		}
	}
	slices.SortStableFunc(out, func(a, b cleanupRow) int {
		return cmp.Or(strings.Compare(a.Unit, b.Unit), strings.Compare(a.Workflow, b.Workflow))
	})
	return out
}

func TestCleanupWorkflowsLoad(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	l := te.loadedIntents()
	var names []string
	for _, w := range l.Workflows {
		if strings.HasPrefix(w.Name, "session-") || strings.HasPrefix(w.Name, "workspace-") || strings.HasPrefix(w.Name, "hygiene-") {
			names = append(names, w.Name)
		}
	}
	if len(names) != 16 {
		t.Fatalf("%d cleanup workflows: %v; problems %v %v", len(names), names, l.Problems, l.BrokenWorkflows)
	}
}

// matchExpected compares rows with expected.json's rows of their clock, or
// rewrites those rows under -update.
func matchExpected(t *testing.T, clock string, rows []cleanupRow, clockB time.Time) {
	t.Helper()
	path := filepath.Join(cleanupFixture, "expected.json")
	var e cleanupExpected
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, &e); err != nil {
			t.Fatal(err)
		}
	}
	if *update {
		e.ClockA = clockA.Format(time.RFC3339)
		if !clockB.IsZero() {
			e.ClockB = clockB.Format(time.RFC3339)
		}
		e.Rows = append(slices.DeleteFunc(e.Rows, func(r cleanupRow) bool { return r.Clock == clock }), rows...)
		slices.SortStableFunc(e.Rows, func(a, b cleanupRow) int { return strings.Compare(a.Clock, b.Clock) })
		raw, err := json.MarshalIndent(e, "", " ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if !clockB.IsZero() && e.ClockB != clockB.Format(time.RFC3339) {
		t.Errorf("clock B %s, expected.json says %s", clockB.Format(time.RFC3339), e.ClockB)
	}
	want := slices.DeleteFunc(slices.Clone(e.Rows), func(r cleanupRow) bool { return r.Clock != clock })
	if slices.Equal(rows, want) {
		return
	}
	for _, r := range rows {
		if !slices.Contains(want, r) {
			t.Errorf("unexpected row %+v", r)
		}
	}
	for _, r := range want {
		if !slices.Contains(rows, r) {
			t.Errorf("missing row %+v", r)
		}
	}
}

// checkScope is the stop rule: every scoped session and worktree has a row,
// and one with no item names its threshold, one held names its guard.
func (f *fixtureEnv) checkScope(t *testing.T, rows []cleanupRow) {
	t.Helper()
	var scoped []string
	for _, w := range f.workspaces {
		if w.Scope == "cleaned" || w.Scope == "blocked" {
			scoped = append(scoped, workspaceUnit(w.Path).String())
		}
	}
	var terms []fxTerminal
	readFixture(t, cleanupFixture, "terminals.json", &terms)
	for _, tm := range terms {
		id, ok, err := f.te.repo.SessionOf(context.Background(), f.mgr.Name(), tm.Handle)
		if err != nil || !ok {
			t.Fatalf("no session took %s: %v", tm.Handle, err)
		}
		scoped = append(scoped, "session:"+id)
	}
	if len(scoped) != 14+13 {
		t.Fatalf("%d scoped units, want the 14 worktrees and 13 sessions", len(scoped))
	}
	for _, u := range scoped {
		i := slices.IndexFunc(rows, func(r cleanupRow) bool { return r.Unit == u })
		if i < 0 {
			t.Errorf("%s has no row", u)
			continue
		}
		for _, r := range rows {
			if r.Unit != u {
				continue
			}
			switch r.Decision {
			case "not matched":
				if r.Threshold == "" {
					t.Errorf("%s: not matched without a threshold", u)
				}
			case string(workflow.DecisionBlocked), string(workflow.DecisionPending), string(workflow.DecisionSkipped):
				if r.Reason == "" {
					t.Errorf("%s: %s without a reason", u, r.Decision)
				}
			}
		}
	}
}

func evaluate(t *testing.T, eng *automation.Engine) []workflow.Item {
	t.Helper()
	plan, err := eng.Evaluate(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Errors) > 0 {
		t.Fatalf("plan errors: %v", plan.Errors)
	}
	return plan.Items
}

func itemOf(t *testing.T, items []workflow.Item, u unit.Unit, name string) workflow.Item {
	t.Helper()
	for _, it := range items {
		if it.Unit == u && it.Workflow == name {
			return it
		}
	}
	t.Fatalf("no %s item on %s", name, u)
	return workflow.Item{}
}

func workspaceUnit(path string) unit.Unit { return wsdomain.UnitOf(path) }

func sweepOnce(t *testing.T, eng *automation.Engine) {
	t.Helper()
	if _, _, err := eng.Sweep(context.Background(), &sync.Mutex{}); err != nil {
		t.Fatal(err)
	}
}

func TestCleanupPlanAtTheFixtureClock(t *testing.T) {
	f := loadFixture(t, cleanupFixture, clockA)
	rows := f.rows(t, f.engine(t), "A")
	f.checkScope(t, rows)
	matchExpected(t, "A", rows, time.Time{})
}

// workHolding are the worktrees that hold work, with the workflow that holds
// each and what its reason says.
var workHolding = []struct {
	suffix, workflow string
	reason           []string
}{
	{"widgets/REVIEW-feat-batch-upload", "workspace-review-finished", []string{"modified files", "commits on no remote", "4 commits not pushed", "back them up with: git push origin HEAD:refs/heads/me/backup-feat/batch-upload"}},
	{"widgets/me-runbook-command", "workspace-idle-no-pr", []string{"workspace.pendency [commits on no remote]", "3 commits not pushed", "back them up with: git push origin HEAD:refs/heads/me/backup-chore/runbook-command"}},
	{"handbook/catalog", "workspace-idle-no-pr", []string{"workspace.pendency [modified files, commits on no remote]", "3 commits not pushed", "back them up with: git push origin HEAD:refs/heads/me/backup-worktree-catalog"}},
	{"handbook/REVIEW-docs-style-guide", "workspace-review-finished", []string{"workspace.pendency [untracked files]", "threads.txt"}},
}

// laterClock is the latest last_activity of the work-holding cases plus 15
// days: past every threshold that applies to them.
func (f *fixtureEnv) laterClock(t *testing.T, eng *automation.Engine) time.Time {
	t.Helper()
	var latest time.Time
	for _, c := range workHolding {
		facts, err := eng.Units.Facts(context.Background(), workspaceUnit(f.workspace(t, c.suffix).Path))
		if err != nil {
			t.Fatal(err)
		}
		if at, _ := facts["workspace.last_activity"].(time.Time); at.After(latest) {
			latest = at
		}
	}
	return latest.Add(15 * 24 * time.Hour)
}

func TestWorkspacesHoldingWorkWaitPastEveryThreshold(t *testing.T) {
	f := loadFixture(t, cleanupFixture, clockA)
	eng := f.engine(t)
	f.now = f.laterClock(t, eng)
	if err := f.markFetched(); err != nil {
		t.Fatal(err)
	}
	rows := f.rows(t, eng, "B")
	f.checkScope(t, rows)
	matchExpected(t, "B", rows, f.now)
	items := evaluate(t, eng)
	for _, c := range workHolding {
		it := itemOf(t, items, workspaceUnit(f.workspace(t, c.suffix).Path), c.workflow)
		if it.Decision != workflow.DecisionPending || it.Default != workflow.OutcomeWait {
			t.Errorf("%s: %s with default %q (%s)", c.suffix, it.Decision, it.Default, it.Reason)
		}
		for _, want := range c.reason {
			if !strings.Contains(it.Reason, want) {
				t.Errorf("%s: reason %q lacks %q", c.suffix, it.Reason, want)
			}
		}
	}
	for _, name := range []string{"workspace-review-finished", "workspace-author-finished", "workspace-temporary", "workspace-duplicate"} {
		f.setMode(name, workflow.ModeApply)
	}
	sweepOnce(t, eng)
	sweepOnce(t, eng)
	for _, c := range workHolding {
		if slices.Contains(f.removed(), f.workspace(t, c.suffix).ID) {
			t.Errorf("%s was removed", c.suffix)
		}
	}

	f.te.cfg.Cleanup.Disposable = append(slices.Clone(f.te.cfg.Cleanup.Disposable), "threads.txt")
	eng = f.engine(t)
	docs := f.workspace(t, "handbook/REVIEW-docs-style-guide")
	if it := itemOf(t, evaluate(t, eng), workspaceUnit(docs.Path), "workspace-review-finished"); it.Decision != workflow.DecisionRun {
		t.Fatalf("with threads.txt disposable: %s (%s)", it.Decision, it.Reason)
	}
	sweepOnce(t, eng)
	if !slices.Contains(f.removed(), docs.ID) {
		t.Fatalf("the review workspace was not removed: %v", f.removed())
	}
}

func (f *fixtureEnv) markFetched() error {
	for _, w := range f.workspaces {
		if err := f.db.Fetches().Mark(context.Background(), w.ID, f.now); err != nil {
			return err
		}
	}
	return nil
}

func TestShippedModesHoldTheRemovals(t *testing.T) {
	f := loadFixture(t, cleanupFixture, clockA)
	eng := f.engine(t)
	f.now = f.laterClock(t, eng)
	if err := f.markFetched(); err != nil {
		t.Fatal(err)
	}
	sweepOnce(t, eng)
	f.now = f.now.Add(5 * time.Minute)
	sweepOnce(t, eng)
	if got := f.removed(); len(got) != 0 {
		t.Fatalf("the shipped modes removed %v", got)
	}
	items, err := eng.Ledger.Items(context.Background(), automation.Filter{Provider: unit.Workspace})
	if err != nil {
		t.Fatal(err)
	}
	removals := 0
	for _, it := range items {
		if it.Steps[0].Use != "workspace.remove" {
			continue
		}
		removals++
		if it.Decision == workflow.DecisionRun {
			t.Errorf("%s on %s runs", it.Workflow, it.Unit)
		}
	}
	if removals == 0 {
		t.Fatal("no removal was planned")
	}
}

func TestHygieneDismissesTheTurnOfAMergedPRAndRefusesAnAnswerToAGoneWorkspace(t *testing.T) {
	f := loadFixture(t, cleanupFixture, clockA)
	ctx := context.Background()
	eng := f.engine(t)
	sweepOnce(t, eng)
	f.now = f.now.Add(3 * time.Minute)
	sweepOnce(t, eng)
	f.now = f.now.Add(3 * time.Minute)
	sweepOnce(t, eng)

	merged := unit.Unit{Provider: unit.GitHub, Key: "acme/gadgets#123"}
	books, err := f.te.repo.Books(ctx, merged)
	if err != nil {
		t.Fatal(err)
	}
	b := books[merged]
	if n, _ := b.Newest(); n == nil || n.State != turn.Dismissed {
		t.Fatalf("the merged PR's asked turn: %+v", n)
	}
	evs, err := f.db.Events().Since(ctx, 0, []string{turns.KindTurnDismissedByRule})
	if err != nil {
		t.Fatal(err)
	}
	if i := slices.IndexFunc(evs, func(e workflow.Event) bool { return e.Unit == merged }); i < 0 || evs[i].Data != "merged" {
		t.Fatalf("dismissal events %+v", evs)
	}

	const probe = "term_00000000-0000-4000-a000-000000000050"
	sess, ok, err := f.te.repo.Session(ctx, probe)
	if err != nil || !ok || sess.ClosedAt.IsZero() {
		t.Fatalf("the gone probe session, dormant after two censuses, then closed: %+v %v %v", sess, ok, err)
	}

	// The fast job delivers the probe's queued answer; its workspace is gone,
	// so the answer is refused once and the job stops retrying it.
	var logged []string
	f.te.Turns.Log = func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }
	probeUnit := unit.Unit{Provider: unit.GitHub, Key: "acme/probe#1"}
	var queued [][]unit.Unit
	for range 2 {
		rep, err := f.te.Turns.Reconcile(ctx, false)
		if err != nil {
			t.Fatal(err)
		}
		queued = append(queued, rep.Queued)
		if err := f.te.Turns.Deliver(ctx, rep.Queued...); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Contains(queued[0], probeUnit) || len(queued[1]) != 0 {
		t.Fatalf("queued by the reconciles: %v", queued)
	}
	pb, err := f.te.repo.Books(ctx, probeUnit)
	if err != nil {
		t.Fatal(err)
	}
	if a := pb[probeUnit].Answers; len(a) != 1 || a[0].Route != turn.Refused {
		t.Fatalf("the probe's answer: %+v", a)
	}
	routed, err := f.db.Events().Since(ctx, 0, []string{turns.KindAnswerRouted})
	if err != nil {
		t.Fatal(err)
	}
	routed = slices.DeleteFunc(routed, func(e workflow.Event) bool { return e.Unit != probeUnit })
	if len(routed) != 1 || routed[0].Data != "refused: the workspace is gone" {
		t.Fatalf("routed events %+v", routed)
	}
	for _, l := range logged {
		if strings.HasPrefix(l, "queued answers:") {
			t.Errorf("logged %q", l)
		}
	}
}

func TestATurnOpenWhenThePRClosedIsDismissed(t *testing.T) {
	f := loadFixture(t, cleanupFixture, clockA)
	ctx := context.Background()
	refs := map[string]string{"acme/widgets#9001": "sent", "acme/widgets#9002": "started"}
	for key, state := range refs {
		ref, _ := pr.ParseKey(key)
		if err := f.te.Store.UpsertPR(ctx, dinv.PR{Ref: ref, State: "CLOSED", ClosedAt: clockA.Add(-time.Hour), HeadBranch: "x", Reasons: []string{"review-requested"}}); err != nil {
			t.Fatal(err)
		}
		at := clockA.Add(-2 * time.Hour).Format(time.RFC3339)
		ft := fxTurn{Seq: 1, Intent: "review", Round: "r1", Trigger: "automated", SentTo: "term_closed", State: state, SentAt: at, Agent: "claude"}
		if state == "started" {
			ft.Session, ft.StartedAt = "term_closed", at
		}
		f.saveBook(t, fxBook{Unit: key, Turns: []fxTurn{ft}})
	}
	sweepOnce(t, f.engine(t))
	evs, err := f.db.Events().Since(ctx, 0, []string{turns.KindTurnDismissedByRule})
	if err != nil {
		t.Fatal(err)
	}
	for key := range refs {
		u := unit.Unit{Provider: unit.GitHub, Key: key}
		if i := slices.IndexFunc(evs, func(e workflow.Event) bool { return e.Unit == u }); i < 0 || evs[i].Data != "closed" {
			t.Errorf("%s (%s turn): events %+v", key, refs[key], evs)
		}
	}
}

func TestCredentialJSONIdenticalToMainIsRemovable(t *testing.T) {
	f := loadFixture(t, cleanupFixture, clockA)
	eng := f.engine(t)
	f.now = f.laterClock(t, eng)
	w := f.workspace(t, "widgets/REVIEW-feat-dark-mode")
	if it := itemOf(t, evaluate(t, eng), workspaceUnit(w.Path), "workspace-review-finished"); it.Decision != workflow.DecisionPending || it.Reason != "observe mode" {
		t.Fatalf("identical credential.json: %s (%s)", it.Decision, it.Reason)
	}
	path := filepath.Join(f.paths.real(w.Path), "credential.json")
	if err := os.WriteFile(path, []byte("another secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	forgetGit()
	t.Cleanup(func() {
		os.WriteFile(path, []byte("secret\n"), 0o644)
		forgetGit()
	})
	f.now = f.now.Add(time.Duration(config.Defaults().Sweep.Slow))
	if it := itemOf(t, evaluate(t, eng), workspaceUnit(w.Path), "workspace-review-finished"); it.Decision != workflow.DecisionBlocked || !strings.Contains(it.Reason, "credential.json") {
		t.Fatalf("a differing credential.json: %s (%s)", it.Decision, it.Reason)
	}
}

func TestManualSessionsWaitForTheUser(t *testing.T) {
	f := loadFixture(t, cleanupFixture, clockA)
	eng := f.engine(t)
	f.now = f.laterClock(t, eng)
	var sess []fxSession
	readFixture(t, cleanupFixture, "sessions.json", &sess)
	var terms []fxTerminal
	readFixture(t, cleanupFixture, "terminals.json", &terms)
	done := map[string]bool{}
	for _, tm := range terms {
		done[tm.Handle] = tm.AgentState == "done"
	}
	var manual []string
	for _, s := range sess {
		if !s.Managed && done[s.Handle] {
			manual = append(manual, s.Handle)
		}
	}
	if len(manual) == 0 {
		t.Fatal("no registered manual session")
	}
	items := evaluate(t, eng)
	for _, h := range manual {
		it := itemOf(t, items, unit.Unit{Provider: unit.Session, Key: h}, "session-manual-quiet")
		if it.Decision != workflow.DecisionPending || it.Default != workflow.OutcomeWait {
			t.Errorf("%s: %s with default %q (%s)", h, it.Decision, it.Default, it.Reason)
		}
	}
	sweepOnce(t, eng)
	for _, h := range manual {
		if slices.Contains(f.sessions.Closed(), h) {
			t.Errorf("%s was closed", h)
		}
	}
}

// cleanupRig is one bundled workflow, with the config's mode overrides, over
// fake facts and steps.
func cleanupRig(t *testing.T, name string, mode workflow.Mode) (*automation.Engine, *automationtest.Units, *automationtest.Steps) {
	t.Helper()
	te := newTestEnv(t, &fake.Manager{})
	if mode != "" {
		te.cfg.Workflows = map[string]config.WorkflowConfig{name: {Mode: string(mode)}}
	}
	i := slices.IndexFunc(applyConfig(te.loadedIntents().Workflows, te.cfg), func(w workflow.Workflow) bool { return w.Name == name })
	if i < 0 {
		t.Fatalf("no bundled workflow %s", name)
	}
	w := applyConfig(te.loadedIntents().Workflows, te.cfg)[i]
	units := automationtest.NewUnits()
	steps := automationtest.NewSteps(map[string]workflow.Reach{w.Steps[0].Use: workflow.ReachLocal})
	eng := &automation.Engine{Workflows: func() []workflow.Workflow { return []workflow.Workflow{w} }, Units: units, Steps: steps,
		Ledger: automationtest.NewLedger(), Clock: automationtest.NewClock(clockA), Config: func() workflow.Facts { return configFacts(te.cfg) }}
	return eng, units, steps
}

var finishedWorkspace = unit.Unit{Provider: unit.Workspace, Key: "ws1"}

func workspaceFactsOf(role, state string, finished time.Duration) workflow.Facts {
	return workflow.Facts{"workspace.role": role, "workspace.main": false, "workspace.pr": "acme/api#7", "workspace.pr_state": state,
		"workspace.pr_finished_for": finished, "workspace.path_exists": true, "workspace.temporary": false, "workspace.duplicate": false,
		"workspace.dirty": 0, "workspace.untracked": 0, "workspace.unpushed": 0, "workspace.newest_turn_state": "", "workspace.open_turn": false,
		"workspace.pendency": []string{}, "workspace.sessions": 0, "workspace.quiet_for": time.Duration(0),
		"workspace.fingerprint": "ws1:c0ffee", "workspace.repo": "acme/api"}
}

// planOf is the engine's items on finishedWorkspace.
func planOf(t *testing.T, eng *automation.Engine) []workflow.Item {
	t.Helper()
	plan, err := eng.Evaluate(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Errors) > 0 {
		t.Fatalf("errors %v", plan.Errors)
	}
	return plan.Items
}

func TestReviewFinishedMatchesOnlyReviewWorkspacesPastGrace(t *testing.T) {
	for name, c := range map[string]struct {
		facts workflow.Facts
		match bool
	}{
		"past grace":        {workspaceFactsOf("review", "merged", 25*time.Hour), true},
		"closed past grace": {workspaceFactsOf("review", "closed", 25*time.Hour), true},
		"inside the grace":  {workspaceFactsOf("review", "merged", 23*time.Hour), false},
		"an author one":     {workspaceFactsOf("author", "merged", 25*time.Hour), false},
		"an open one":       {workspaceFactsOf("review", "open", 0), false},
		"a main checkout": {func() workflow.Facts {
			f := workspaceFactsOf("review", "merged", 25*time.Hour)
			f["workspace.main"] = true
			return f
		}(), false},
	} {
		eng, units, _ := cleanupRig(t, "workspace-review-finished", "")
		units.Put(finishedWorkspace, c.facts)
		if got := len(planOf(t, eng)) == 1; got != c.match {
			t.Errorf("%s: matched %v", name, got)
		}
	}
}

func TestReviewFinishedWaitsOnDirtyUntrackedOrUnpushed(t *testing.T) {
	for _, c := range []struct{ fact, refusal string }{
		{"workspace.dirty", "1 modified tracked file in /w/ws1"},
		{"workspace.untracked", "untracked or ignored files outside the disposable list: notes.txt"},
		{"workspace.unpushed", "2 commits not pushed in /w/ws1; back them up with: git push origin HEAD:refs/heads/me/backup-b"},
	} {
		eng, units, steps := cleanupRig(t, "workspace-review-finished", workflow.ModeApply)
		f := workspaceFactsOf("review", "merged", 25*time.Hour)
		f[c.fact] = 1
		units.Put(finishedWorkspace, f)
		steps.Refuse("workspace.remove", finishedWorkspace, c.refusal)
		items := planOf(t, eng)
		if len(items) != 1 || items[0].Decision != workflow.DecisionPending || items[0].Default != workflow.OutcomeWait || items[0].Reason != c.fact+" 1: "+c.refusal {
			t.Errorf("%s: %+v", c.fact, items)
		}
		sweepOnce(t, eng)
		if len(steps.Ran()) != 0 {
			t.Errorf("%s: ran %v", c.fact, steps.Ran())
		}
	}
}

func TestIdleNoPRWaitsWithItsPendency(t *testing.T) {
	eng, units, steps := cleanupRig(t, "workspace-idle-no-pr", "")
	f := workspaceFactsOf("none", "", 0)
	f["workspace.pr"], f["workspace.quiet_for"] = "", 15*24*time.Hour
	f["workspace.unpushed"], f["workspace.pendency"] = 3, []string{"commits on no remote"}
	units.Put(finishedWorkspace, f)
	refusal := "3 commits not pushed in /w/ws1; back them up with: git push origin HEAD:refs/heads/me/backup-b"
	steps.Refuse("workspace.remove", finishedWorkspace, refusal)
	items := planOf(t, eng)
	if len(items) != 1 || items[0].Decision != workflow.DecisionPending || items[0].Default != workflow.OutcomeWait || items[0].Reason != "workspace.pendency [commits on no remote]: "+refusal {
		t.Fatalf("items %+v", items)
	}
	f["workspace.sessions"] = 1
	units.Put(finishedWorkspace, f)
	if items := planOf(t, eng); len(items) != 0 {
		t.Fatalf("with a live terminal: %+v", items)
	}
}

func TestAuthorFinishedRemovesACleanWorkspaceWithNoPendency(t *testing.T) {
	eng, units, steps := cleanupRig(t, "workspace-author-finished", workflow.ModeApply)
	units.Put(finishedWorkspace, workspaceFactsOf("author", "merged", 25*time.Hour))
	if items := planOf(t, eng); len(items) != 1 || items[0].Decision != workflow.DecisionRun {
		t.Fatalf("items %+v", items)
	}
	sweepOnce(t, eng)
	if len(steps.Ran()) != 1 {
		t.Fatalf("ran %v", steps.Ran())
	}
}

func TestAuthorFinishedWaitsOnEachPendency(t *testing.T) {
	for _, c := range []struct{ pendency, refusal string }{
		{factworkspace.PendingModified, "1 modified tracked file in /w/ws1"},
		{factworkspace.PendingUntracked, "untracked or ignored files outside the disposable list: a.txt"},
		{factworkspace.PendingUnpushed, "2 commits not pushed in /w/ws1; back them up with: git push origin HEAD:refs/heads/me/backup-b"},
		{factworkspace.PendingAsked, ""},
		{factworkspace.PendingFailed, ""},
		{factworkspace.PendingUnrecorded, ""},
		{factworkspace.PendingOpen, ""},
	} {
		eng, units, steps := cleanupRig(t, "workspace-author-finished", workflow.ModeApply)
		f := workspaceFactsOf("author", "closed", 25*time.Hour)
		f["workspace.pendency"] = []string{c.pendency}
		units.Put(finishedWorkspace, f)
		reason := "workspace.pendency [" + c.pendency + "]"
		if c.refusal != "" {
			steps.Refuse("workspace.remove", finishedWorkspace, c.refusal)
			reason += ": " + c.refusal
		}
		items := planOf(t, eng)
		if len(items) != 1 || items[0].Decision != workflow.DecisionPending || items[0].Default != workflow.OutcomeWait || items[0].Reason != reason {
			t.Errorf("%s: %+v", c.pendency, items)
		}
		sweepOnce(t, eng)
		if len(steps.Ran()) != 0 {
			t.Errorf("%s: ran %v", c.pendency, steps.Ran())
		}
	}
}

func TestAuthorFinishedSkipsReviewWorkspacesAndOpenPRs(t *testing.T) {
	for name, f := range map[string]workflow.Facts{
		"a review workspace":  workspaceFactsOf("review", "merged", 25*time.Hour),
		"an open PR":          workspaceFactsOf("author", "open", 0),
		"no pull request":     workspaceFactsOf("none", "", 0),
		"inside of the grace": workspaceFactsOf("author", "merged", time.Hour),
	} {
		eng, units, _ := cleanupRig(t, "workspace-author-finished", workflow.ModeApply)
		units.Put(finishedWorkspace, f)
		if items := planOf(t, eng); len(items) != 0 {
			t.Errorf("%s: %+v", name, items)
		}
	}
}

func TestSessionGoneClosesOnlyTheSessionWhoseWorktreeIsGone(t *testing.T) {
	ctx := context.Background()
	expired := []string{"term_00000000-0000-4000-a000-000000000050"}

	f := loadFixture(t, cleanupFixture, clockA)
	eng := f.engine(t)
	sweepOnce(t, eng)
	f.now = f.now.Add(3 * time.Minute)
	sweepOnce(t, eng)
	f.now = f.now.Add(3 * time.Minute)
	sweepOnce(t, eng)
	var closed []string
	var sess []fxSession
	readFixture(t, cleanupFixture, "sessions.json", &sess)
	for _, s := range sess {
		row, ok, err := f.te.repo.Session(ctx, s.Handle)
		if err != nil || !ok {
			t.Fatal(s.Handle, ok, err)
		}
		if !row.ClosedAt.IsZero() && !slices.Contains(f.sessions.Closed(), s.Handle) {
			closed = append(closed, s.Handle)
		}
	}
	slices.Sort(expired)
	if len(expired) == 0 || !slices.Equal(closed, expired) {
		t.Fatalf("the workflow marked %v closed, want %v", closed, expired)
	}
}
