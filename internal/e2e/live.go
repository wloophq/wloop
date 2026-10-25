//go:build live

package e2e

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/agent/claude"
	"github.com/wloophq/wloop/internal/adapter/store/storetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/cli"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/workspace"
	wsorca "github.com/wloophq/wloop/internal/workspace/orca"
)

const (
	poll             = 2 * time.Second
	commandWait      = 5 * time.Minute
	worktreeGoneWait = 30 * time.Second
)

// live is one live test's view of the machine: the installed daemon, Orca,
// and what the test created, which is all it may remove.
type live struct {
	t      *testing.T
	stamp  string
	socket string
	daemon *client.Client
	orca   *wsorca.Manager

	mu sync.Mutex
	// terminals are the handles this test received; only those are closed.
	terminals map[string]bool
}

func newLive(t *testing.T) *live {
	t.Helper()
	t.Setenv(wsorca.LiveVar, "1")
	socket := config.DaemonSocket()
	c := client.New(socket)
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("the installed daemon does not answer on %s: %v", socket, err)
	}
	return &live{t: t, stamp: time.Now().Format("20060102-150405"), socket: socket, daemon: c,
		orca: &wsorca.Manager{R: wsorca.Exec{}}, terminals: map[string]bool{}}
}

// name is a name the guards accept: the prefix, this run's stamp and what.
func (l *live) name(what string) string { return Prefix + l.stamp + "-" + what }

// run runs a command and returns its standard output; it fails the test
// when the command fails.
func run(t *testing.T, dir, name string, args ...string) []byte {
	t.Helper()
	out, err := try(dir, name, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func try(dir, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandWait)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// command runs the installed binary.
func command(t *testing.T, args ...string) []byte {
	t.Helper()
	return run(t, "", "gh", append([]string{brand.Name}, args...)...)
}

// ghRead runs a gh command that writes nothing.
func ghRead(t *testing.T, args ...string) []byte {
	t.Helper()
	return run(t, "", "gh", args...)
}

// ghWrite is the only way a live test writes to GitHub: it refuses any
// repository but the sandbox before running gh against it.
func ghWrite(t *testing.T, repo string, args ...string) []byte {
	t.Helper()
	if err := SandboxOnly(repo); err != nil {
		t.Fatal(err)
	}
	return run(t, "", "gh", append(args, "-R", repo)...)
}

// gitPush pushes an owned branch of a sandbox checkout.
func gitPush(t *testing.T, dir, branch string) {
	t.Helper()
	if err := MustOwnBranch(branch); err != nil {
		t.Fatal(err)
	}
	remote := strings.TrimSpace(string(run(t, dir, "git", "remote", "get-url", "origin")))
	if err := SandboxOnly(repoOfRemote(remote)); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "git", "push", "-u", "origin", branch)
}

func repoOfRemote(remote string) string {
	s := strings.TrimSuffix(strings.TrimSpace(remote), ".git")
	for _, p := range []string{"git@github.com:", "https://github.com/", "ssh://git@github.com/"} {
		s = strings.TrimPrefix(s, p)
	}
	return s
}

// waitFor polls fn every two seconds until it holds, and fails with the
// last observation when timeout passes first.
func waitFor(t *testing.T, timeout time.Duration, what string, fn func() (bool, string)) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		ok, seen := fn()
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: not within %v; last observed: %s", what, timeout, seen)
		}
		time.Sleep(poll)
	}
}

// orcaRepo is the Orca repository whose remote is owner/repo.
func (l *live) orcaRepo(t *testing.T, slug string) wsdomain.Repo {
	t.Helper()
	repos, err := l.orca.Repos(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range repos {
		if strings.EqualFold(r.RemoteKey, "github.com/"+slug) {
			return r
		}
	}
	t.Fatalf("Orca lists no repository of %s", slug)
	return wsdomain.Repo{}
}

// guarded is ws as the guard reads it.
func (l *live) guarded(t *testing.T, ws wsdomain.Workspace) Workspace {
	t.Helper()
	repos, err := l.orca.Repos(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	g := Workspace{Name: ws.DisplayName, PR: ws.LinkedPR}
	for _, r := range repos {
		if r.ID == ws.RepoID {
			g.Repo = strings.TrimPrefix(r.RemoteKey, "github.com/")
		}
	}
	return g
}

// createWorkspace creates an Orca workspace named name in the repository
// owner/repo, and removes it with its local branch when the test ends.
func (l *live) createWorkspace(t *testing.T, slug, name string) wsdomain.Workspace {
	t.Helper()
	if err := MustOwn(name); err != nil {
		t.Fatal(err)
	}
	repo := l.orcaRepo(t, slug)
	ws, _, err := l.orca.Create(context.Background(), workspace.CreateRequest{RepoID: repo.ID, Name: name})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.removeWorkspace(t, repo.Path, ws) })
	return ws
}

// removeWorkspace removes a workspace the guard accepts, and its local
// branch in repoPath, the checkout Orca made the workspace from, when the
// branch is the test's own.
func (l *live) removeWorkspace(t *testing.T, repoPath string, ws wsdomain.Workspace) {
	t.Helper()
	if err := MustOwnWorkspace(l.guarded(t, ws)); err != nil {
		t.Error(err)
		return
	}
	if err := l.orca.Remove(context.Background(), ws.ID, true); err != nil && !strings.Contains(err.Error(), "not_found") {
		t.Errorf("removing workspace %s: %v", ws.DisplayName, err)
	}
	if ws.Branch == "" || MustOwnBranch(ws.Branch) != nil || repoPath == "" {
		return
	}
	if !waitWorktreeGone(repoPath, ws.Path) {
		t.Logf("worktree %s is still listed, keeping branch %s", ws.Path, ws.Branch)
		return
	}
	if out, err := try(repoPath, "git", "branch", "--list", "--", ws.Branch); err != nil || strings.TrimSpace(string(out)) == "" {
		return
	}
	if _, err := try(repoPath, "git", "branch", "-D", "--", ws.Branch); err != nil {
		t.Logf("deleting branch %s: %v", ws.Branch, err)
	}
}

// waitWorktreeGone waits until git no longer lists path as a worktree of
// repoPath, and reports whether it went away in time.
func waitWorktreeGone(repoPath, path string) bool {
	canon := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return p
	}
	want := canon(path)
	listed := func() bool {
		out, err := try(repoPath, "git", "worktree", "list", "--porcelain")
		if err != nil {
			return true
		}
		for _, line := range strings.Split(string(out), "\n") {
			if p, ok := strings.CutPrefix(line, "worktree "); ok && (p == path || canon(p) == want) {
				return true
			}
		}
		return false
	}
	deadline := time.Now().Add(worktreeGoneWait)
	for listed() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(time.Second)
	}
	return true
}

// workspaceByID is the listed workspace with id, false when Orca lists none.
func (l *live) workspaceByID(t *testing.T, id string) (wsdomain.Workspace, bool) {
	t.Helper()
	all, err := l.orca.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range all {
		if w.ID == id {
			return w, true
		}
	}
	return wsdomain.Workspace{}, false
}

// own marks a terminal handle the test received, so it may close it.
func (l *live) own(handle string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.terminals[handle] = true
}

// closeTerminal closes a terminal by its handle, only one the test received.
func (l *live) closeTerminal(t *testing.T, handle string) {
	t.Helper()
	l.mu.Lock()
	owned := l.terminals[handle]
	l.mu.Unlock()
	if !owned {
		t.Fatalf("refusing to close terminal %s: the test did not receive it", handle)
	}
	raw, err := wsorca.Exec{}.Run(context.Background(), "terminal", "close", "--terminal", handle, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		OK    bool            `json:"ok"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil || !env.OK {
		t.Fatalf("closing terminal %s: %s", handle, raw)
	}
}

// listed says whether Orca still lists handle as a usable terminal of the
// workspace.
func (l *live) listed(t *testing.T, workspaceID, handle string) bool {
	t.Helper()
	ts, err := l.orca.Terminals(context.Background(), workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	return slices.ContainsFunc(ts, func(x wsorca.Terminal) bool { return x.Handle == handle })
}

// liveBook is the pull request's book as `turn show` prints it, flattened
// into a turn.Book, with the turns' stored instructions and the events.
type liveBook struct {
	turn.Book
	shown  api.TurnBook
	Events []storetest.Event
}

func (b liveBook) instructions(round string) string {
	for _, s := range b.shown.Turns {
		if s.Turn.Round == round {
			return s.Instructions
		}
	}
	return ""
}

func (b liveBook) events(kind string) []storetest.Event {
	var out []storetest.Event
	for _, e := range b.Events {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// record is the newest record of turn seq whose summary is summary.
func (b liveBook) record(seq int64, summary string) (turn.Record, bool) {
	for i := len(b.Records) - 1; i >= 0; i-- {
		if r := b.Records[i]; r.TurnSeq == seq && r.Record.Summary == summary {
			return r, true
		}
	}
	return turn.Record{}, false
}

func (b liveBook) answer(seq int64, optionID string) (turn.Answer, bool) {
	for i := len(b.Answers) - 1; i >= 0; i-- {
		if a := b.Answers[i]; a.TurnSeq == seq && a.OptionID == optionID {
			return a, true
		}
	}
	return turn.Answer{}, false
}

// bookOf reads the pull request's book through the turn show command, and
// its events from the daemon's store; empty while it has no turn.
func bookOf(t *testing.T, url string) liveBook {
	t.Helper()
	raw, err := try("", "gh", brand.Name, "turn", "show", url, "--json")
	if err != nil && strings.Contains(err.Error(), "no turn of ") {
		return liveBook{}
	}
	if err != nil {
		t.Fatal(err)
	}
	var shown api.TurnBook
	if err := json.Unmarshal(raw, &shown); err != nil {
		t.Fatalf("turn show %s: %v\n%.2000s", url, err, raw)
	}
	b := liveBook{shown: shown}
	b.Unit = shown.PR.Unit()
	for _, s := range shown.Turns {
		b.Turns = append(b.Turns, s.Turn)
		b.Records = append(b.Records, s.Records...)
		b.Answers = append(b.Answers, s.Answers...)
	}
	slices.SortStableFunc(b.Records, func(x, y turn.Record) int { return cmp.Or(x.At.Compare(y.At), cmp.Compare(x.ID, y.ID)) })
	slices.SortStableFunc(b.Answers, func(x, y turn.Answer) int { return cmp.Or(x.At.Compare(y.At), cmp.Compare(x.ID, y.ID)) })
	ctx, cancel := context.WithTimeout(context.Background(), commandWait)
	defer cancel()
	b.Events = storetest.Events(ctx, t, filepath.Join(config.StateDir(), "store.db"), shown.PR)
	return b
}

// answer answers the open question of the pull request through the UserAPI
// answer action, as the cockpit, a notification or the TUI do. A 409 (the
// session is busy, or the question is not recorded yet) is retried for two
// minutes; any other refusal fails the test.
func (l *live) answer(t *testing.T, ref pr.Ref, optionID string, source turn.Source) {
	t.Helper()
	l.answerWith(t, ref, api.ActionRequest{OptionID: optionID, Source: string(source)})
}

// answerWith posts req as the answer on ref and returns the daemon's output.
func (l *live) answerWith(t *testing.T, ref pr.Ref, req api.ActionRequest) string {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var out string
	url := "http://" + brand.Name + api.UnitPath(ref.Unit(), "answer")
	hc := &http.Client{Transport: client.UnixTransport(l.socket), Timeout: 30 * time.Minute}
	waitFor(t, 2*time.Minute, fmt.Sprintf("the daemon takes the answer %+v on %s", req, ref), func() (bool, string) {
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set(api.Header, api.Version)
		req.Header.Set("Content-Type", "application/json")
		resp, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var msg bytes.Buffer
		_, _ = msg.ReadFrom(resp.Body)
		switch {
		case resp.StatusCode/100 == 2:
			out = msg.String()
			return true, ""
		case resp.StatusCode == http.StatusConflict:
			return false, msg.String()
		}
		t.Fatalf("answering %s with %+v: %s %s", ref, req, resp.Status, msg.String())
		return false, ""
	})
	return out
}

// homes are the agent homes the probe runs on: LOOP_E2E_HOMES, else every
// home the install command finds.
func homes(t *testing.T) []string {
	t.Helper()
	if v := os.Getenv("LOOP_E2E_HOMES"); v != "" {
		return strings.Split(v, ":")
	}
	found := claude.FindHomes(config.Home(), os.Getenv("CLAUDE_CONFIG_DIR"), wsorca.New().ProfileHomes(config.Home(), "claude"))
	if cfg, err := config.Load(cli.Defaults()); err == nil {
		found = append(found, cfg.AgentHomes()...)
	}
	var out []string
	for _, h := range found {
		h = filepath.Clean(config.ExpandHome(h))
		if !slices.Contains(out, h) {
			out = append(out, h)
		}
	}
	if len(out) == 0 {
		t.Fatal("no agent home found; set LOOP_E2E_HOMES")
	}
	return out
}

// homeTag is a short name for a home inside a workspace name.
func homeTag(i int, home string) string {
	tag := strings.ToLower(filepath.Base(filepath.Dir(home)) + "-" + filepath.Base(home))
	tag = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return '-'
	}, tag)
	if len(tag) > 24 {
		tag = tag[len(tag)-24:]
	}
	return fmt.Sprintf("h%d-%s", i, strings.Trim(tag, "-"))
}
