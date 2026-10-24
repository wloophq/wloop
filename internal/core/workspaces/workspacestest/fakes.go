package workspacestest

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/workspaces"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workspace"
)

// Disposable is the disposable list of the fakes' Service.
var Disposable = []string{"node_modules", "build", ".DS_Store", turn.ScratchDir}

var (
	_ workspaces.Manager      = (*Manager)(nil)
	_ workspaces.Remote       = (*Git)(nil)
	_ workspaces.Tree         = (*Git)(nil)
	_ workspaces.SessionGuard = (*Sessions)(nil)
	_ workspaces.Files        = (*Files)(nil)
	_ workspaces.Rounds       = (*Rounds)(nil)
	_ workspaces.FetchLog     = (*FetchLog)(nil)
	_ port.Publisher          = (*Publisher)(nil)
)

// Manager records removals and unregistrations; RemoveErr fails a removal.
type Manager struct {
	Workspaces []workspace.Workspace
	RepoList   []workspace.Repo
	RemoveErr  error

	mu           sync.Mutex
	removed      []string
	forced       []string
	unregistered []string
}

func (m *Manager) List(context.Context) ([]workspace.Workspace, error) {
	return slices.Clone(m.Workspaces), nil
}

func (m *Manager) Repos(context.Context) ([]workspace.Repo, error) {
	return slices.Clone(m.RepoList), nil
}

func (m *Manager) Remove(_ context.Context, id string, force bool) error {
	if m.RemoveErr != nil {
		return m.RemoveErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removed = append(m.removed, id)
	if force {
		m.forced = append(m.forced, id)
	}
	return nil
}

func (m *Manager) Unregister(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.unregistered = append(m.unregistered, id)
	return nil
}

func (m *Manager) Removed() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.removed)
}

func (m *Manager) Forced() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.forced)
}

func (m *Manager) Unregistered() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.unregistered)
}

// Dir is a checkout as Git serves it.
type Dir struct {
	// Branch is tracked on Remote, origin when empty; Gone is its deletion
	// there, which a fetch reveals.
	Remote   string
	Branch   string
	Gone     bool
	Unpushed int
	Head     string
	// HeldBy is the pull request head that contains HEAD.
	HeldBy   string
	Modified int
	// Kept is what Tree.Kept reports.
	Kept               string
	Untracked, Ignored []string
	fetched            bool
}

// Git is an in-memory Remote and Tree over Dirs. FetchErr fails every
// fetch; AfterUnpushed runs after each Unpushed read, as a commit landing
// then would.
type Git struct {
	Dirs          map[string]*Dir
	FetchErr      error
	AfterUnpushed func(dir string, d *Dir)

	mu      sync.Mutex
	fetches []string
}

func (g *Git) dir(dir string) (*Dir, error) {
	d, ok := g.Dirs[dir]
	if !ok {
		return nil, errors.New("not a git repository: " + dir)
	}
	return d, nil
}

func (g *Git) Tracking(_ context.Context, dir string) (string, string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	d, err := g.dir(dir)
	if err != nil {
		return "", "", err
	}
	if d.Remote == "" {
		return "origin", d.Branch, nil
	}
	return d.Remote, d.Branch, nil
}

func (g *Git) FetchTracking(_ context.Context, dir string, number int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.fetches = append(g.fetches, fmt.Sprintf("%s#%d", dir, number))
	if g.FetchErr != nil {
		return g.FetchErr
	}
	d, err := g.dir(dir)
	if err != nil {
		return err
	}
	d.fetched = true
	return nil
}

// Fetches are the fetches as "<dir>#<pull request number>", in order.
func (g *Git) Fetches() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.fetches)
}

func (g *Git) RemoteHasBranch(_ context.Context, dir, _, branch string) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	d, err := g.dir(dir)
	if err != nil {
		return false, err
	}
	return d.Branch == branch && !(d.Gone && d.fetched), nil
}

func (g *Git) Unpushed(_ context.Context, dir string) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	d, err := g.dir(dir)
	if err != nil {
		return 0, err
	}
	n := d.Unpushed
	if g.AfterUnpushed != nil {
		g.AfterUnpushed(dir, d)
	}
	return n, nil
}

func (g *Git) PRHolds(_ context.Context, dir, prHead string) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	d, err := g.dir(dir)
	if err != nil {
		return false, err
	}
	return prHead != "" && prHead == d.HeldBy, nil
}

func (g *Git) Changed(_ context.Context, dir string) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	d, err := g.dir(dir)
	if err != nil {
		return 0, err
	}
	return d.Modified + len(d.Untracked), nil
}

func (g *Git) DirtyTracked(_ context.Context, dir string) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	d, err := g.dir(dir)
	if err != nil {
		return 0, err
	}
	return d.Modified, nil
}

func (g *Git) Ignored(_ context.Context, dir string) ([]string, []string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	d, err := g.dir(dir)
	if err != nil {
		return nil, nil, err
	}
	return slices.Clone(d.Untracked), slices.Clone(d.Ignored), nil
}

func (g *Git) Kept(_ context.Context, dir string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	d, err := g.dir(dir)
	if err != nil {
		return "", err
	}
	return d.Kept, nil
}

func (g *Git) Head(_ context.Context, dir string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	d, err := g.dir(dir)
	if err != nil {
		return "", err
	}
	return d.Head, nil
}

// Files is an in-memory file tree of slash paths.
type Files struct {
	mu    sync.Mutex
	files map[string]string
	links map[string]bool
}

func NewFiles() *Files { return &Files{files: map[string]string{}, links: map[string]bool{}} }

func (f *Files) Put(p, content string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[path.Clean(p)] = content
}

func (f *Files) AddLink(p string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.links[path.Clean(p)] = true
}

// under is the files below dir by their path relative to it.
func (f *Files) under(dir string) map[string]string {
	out := map[string]string{}
	for p, c := range f.files {
		if rel, ok := strings.CutPrefix(p, dir+"/"); ok {
			out[rel] = c
		}
	}
	return out
}

func (f *Files) Same(a, b string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, b = path.Clean(a), path.Clean(b)
	ca, fileA := f.files[a]
	cb, fileB := f.files[b]
	if fileA || fileB {
		return fileA && fileB && ca == cb, nil
	}
	ua, ub := f.under(a), f.under(b)
	if len(ua) == 0 || len(ub) != len(ua) {
		return false, nil
	}
	for rel, c := range ua {
		if other, ok := ub[rel]; !ok || other != c {
			return false, nil
		}
	}
	return true, nil
}

func (f *Files) Exists(p string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	p = path.Clean(p)
	_, file := f.files[p]
	return file || f.links[p] || len(f.under(p)) > 0
}

func (f *Files) Link(p string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.links[path.Clean(p)]
}

func (f *Files) Repos(dir string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	dir = path.Clean(dir)
	var roots []string
	for p := range f.files {
		if path.Base(p) != ".git" {
			continue
		}
		if p == dir+"/.git" {
			return []string{"."}, nil
		}
		if rel, ok := strings.CutPrefix(path.Dir(p), dir+"/"); ok {
			roots = append(roots, rel)
		}
	}
	slices.Sort(roots)
	return slices.DeleteFunc(roots, func(r string) bool {
		return slices.ContainsFunc(roots, func(o string) bool { return strings.HasPrefix(r, o+"/") })
	}), nil
}

func (c Checkout) ignoredOnly() []string {
	_, ignored := c.IgnoredUntracked()
	return ignored
}

// FakeWorld realizes c in memory: the workspace at /w/ws, its main checkout
// at /w/main.
func FakeWorld(_ *testing.T, c Checkout) World {
	const dir, main, head = "/w/ws", "/w/main", "c0ffee"
	untracked, ignored := c.IgnoredUntracked()
	d := &Dir{Branch: Branch, Gone: c.BranchGone, Unpushed: c.Unpushed, Head: head, Modified: len(c.Modified), Untracked: untracked, Ignored: ignored}
	if c.PRHoldsHead {
		d.HeldBy = head
	}
	g := &Git{Dirs: map[string]*Dir{dir: d}}
	files := NewFiles()
	files.Put(dir+"/.git", "")
	for p, content := range c.Untracked {
		files.Put(dir+"/"+p, content)
	}
	for p, content := range c.Ignored {
		files.Put(dir+"/"+p, content)
	}
	for p, content := range c.Main {
		files.Put(main+"/"+p, content)
	}
	for _, l := range c.Links {
		files.AddLink(dir + "/" + l)
	}
	for name, n := range c.Nested {
		at := dir + "/" + c.NestedPath(name)
		nd := &Dir{Branch: "main", Head: "n1", Ignored: Checkout{Ignored: n.Ignored}.ignoredOnly()}
		if n.Modified {
			nd.Modified = 1
		}
		if n.Kept() {
			nd.Kept = "kept"
		}
		g.Dirs[at] = nd
		files.Put(at+"/.git", "")
		for p, content := range n.Ignored {
			files.Put(at+"/"+p, content)
		}
	}
	return World{Remote: g, Tree: g, Files: files, Dir: dir, Main: main, Head: head}
}

// Sessions refuses a workspace in Refuse and records what it closes.
type Sessions struct {
	Refuse   map[string]string
	CloseErr error

	mu     sync.Mutex
	closed []string
}

func (s *Sessions) Refusal(_ context.Context, id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Refuse[id], nil
}

func (s *Sessions) CloseAll(_ context.Context, id, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.CloseErr != nil {
		return s.CloseErr
	}
	s.closed = append(s.closed, id+": "+reason)
	return nil
}

// Closed is each CloseAll as "<workspace id>: <reason>".
func (s *Sessions) Closed() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.closed)
}

type Rounds struct {
	mu      sync.Mutex
	skipped []unit.Unit
}

func (r *Rounds) SkipFirstRound(_ context.Context, u unit.Unit) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.skipped = append(r.skipped, u)
	return nil
}

func (r *Rounds) Skipped() []unit.Unit {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.skipped)
}

type FetchLog struct {
	mu    sync.Mutex
	last  map[string]time.Time
	marks int
}

func (l *FetchLog) Last(_ context.Context, id string) (time.Time, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.last[id], nil
}

func (l *FetchLog) Mark(_ context.Context, id string, at time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.last == nil {
		l.last = map[string]time.Time{}
	}
	l.last[id] = at
	l.marks++
	return nil
}

func (l *FetchLog) Marks() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.marks
}

type Publisher struct {
	mu     sync.Mutex
	events []port.Event
}

func (p *Publisher) Publish(evs ...port.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, evs...)
}

func (p *Publisher) Events() []port.Event {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.events)
}

type Clock struct{ At time.Time }

func (c Clock) Now() time.Time { return c.At }
