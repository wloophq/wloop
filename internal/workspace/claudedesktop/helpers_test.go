package claudedesktop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/gitx"
	"github.com/wloophq/wloop/internal/workspace"
)

const (
	testUUID    = "d17d2f0e-5c1a-4b8e-9f3a-2b7c6d5e4f10"
	desktopPS   = "21671 Fri Oct 23 20:56:30 2026     /Applications/Claude.app/Contents/MacOS/Claude\n"
	helperPS    = "8199 Fri Oct 23 22:58:49 2026     /Applications/Claude.app/Contents/Helpers/disclaimer\n"
	desktopRTID = "21671@Fri Oct 23 20:56:30 2026"
)

// fakeExec runs git for real and answers ps, claude and open from the test.
type fakeExec struct {
	mu     sync.Mutex
	ps     string
	cmds   []Cmd
	claude func(Cmd) ([]byte, error)
	open   func(Cmd) error
	// fetch stands in for git fetch, which would reach the network.
	fetch func(Cmd) error
}

func (f *fakeExec) Run(ctx context.Context, c Cmd) ([]byte, error) {
	f.mu.Lock()
	f.cmds = append(f.cmds, c)
	ps, claude, open, fetch := f.ps, f.claude, f.open, f.fetch
	f.mu.Unlock()
	switch c.Name {
	case "git":
		if slices.Contains(c.Args, "fetch") {
			if fetch == nil {
				return nil, fmt.Errorf("unexpected %s", c)
			}
			return nil, fetch(c)
		}
		return Exec{}.Run(ctx, c)
	case "ps":
		return []byte(ps), nil
	case "claude":
		if claude == nil {
			return nil, fmt.Errorf("unexpected %s", c)
		}
		return claude(c)
	case "open":
		if open == nil {
			return nil, fmt.Errorf("unexpected %s", c)
		}
		return nil, open(c)
	}
	return nil, fmt.Errorf("unexpected %s", c)
}

func (f *fakeExec) ran(name string) []Cmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Cmd
	for _, c := range f.cmds {
		if c.Name == name {
			out = append(out, c)
		}
	}
	return out
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Sleep(_ context.Context, d time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
	return nil
}

type fixture struct {
	m    *Manager
	exec *fakeExec
	repo string
	dead map[int]bool
	clk  *clock
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	gitEnv(t)
	base := t.TempDir()
	f := &fixture{exec: &fakeExec{ps: helperPS + desktopPS}, dead: map[int]bool{},
		clk: &clock{t: time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)}}
	f.m = &Manager{
		Root:        filepath.Join(base, "worktrees"),
		SessionsDir: filepath.Join(base, "sessions"),
		StoreDir:    filepath.Join(base, "store"),
		AppPath:     "/Applications/Claude.app",
		Exec:        f.exec,
		Environ: func() []string {
			return []string{"PATH=/usr/bin", "CLAUDE_CONFIG_DIR=/x/.claude-other", "CLAUDECODE=1", "HOME=/u"}
		},
		Alive:     func(pid int) bool { return !f.dead[pid] },
		Now:       f.clk.Now,
		Sleep:     f.clk.Sleep,
		ReadyWait: 20 * time.Second,
	}
	for _, d := range []string{f.m.SessionsDir, f.m.StoreDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	f.repo = newRepo(t, filepath.Join(base, "api"))
	return f
}

// gitEnv keeps the user's git configuration, such as commit signing, out of the test repositories.
func gitEnv(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", empty)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.com")
}

func newRepo(t *testing.T, dir string) string {
	t.Helper()
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command(gitx.Binary(), append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	run("init", "-q", "-b", "main")
	run("commit", "-q", "--allow-empty", "-m", "init")
	run("remote", "add", "origin", "git@github.com:acme/api.git")
	run("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/develop")
	real, err := canonical(dir)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

// worktree registers the repository and creates a workspace named name in it.
func (f *fixture) worktree(t *testing.T, name string) wsdomain.Workspace {
	t.Helper()
	ctx := context.Background()
	if _, err := f.m.AddRepo(ctx, f.repo); err != nil {
		t.Fatal(err)
	}
	ws, _, err := f.m.Create(ctx, workspace.CreateRequest{RepoID: f.repo, Name: name})
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func readFixture(t *testing.T, name string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// register writes the recorded registry entry name, with fields replaced, as <pid>.json.
func (f *fixture) register(t *testing.T, name string, fields map[string]any) map[string]any {
	t.Helper()
	e := readFixture(t, name)
	for k, v := range fields {
		e[k] = v
	}
	writeJSON(t, filepath.Join(f.m.SessionsDir, fmt.Sprint(e["pid"])+".json"), e)
	return e
}

func (f *fixture) key(t *testing.T, pid int) {
	t.Helper()
	writeJSON(t, filepath.Join(f.m.SessionsDir, strconv.Itoa(pid)+".3f9a.key"), readFixture(t, "key.json"))
}

func (f *fixture) store(t *testing.T, account string, fields map[string]any) {
	t.Helper()
	s := readFixture(t, "store_session.json")
	for k, v := range fields {
		s[k] = v
	}
	dir := filepath.Join(f.m.StoreDir, account, "org-1")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(dir, s["sessionId"].(string)+".json"), s)
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}
