package claudedesktop

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/turn"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/workspace"
)

const otherUUID = "a1b2c3d4-0000-4000-8000-000000000001"

func TestPanesAndTerminalsCoverDesktopSessionsInListedWorktrees(t *testing.T) {
	f := newFixture(t)
	ws := f.worktree(t, "w")
	f.register(t, "registry_desktop.json", map[string]any{"cwd": ws.Path, "status": "busy"})
	f.register(t, "registry_cli.json", map[string]any{"cwd": ws.Path})
	f.register(t, "registry_desktop.json", map[string]any{"pid": 70001, "cwd": t.TempDir(), "hostSessionId": HandleOf(otherUUID)})
	f.register(t, "registry_desktop.json", map[string]any{"pid": 70002, "cwd": ws.Path, "hostSessionId": HandleOf(otherUUID)})
	f.dead[70002] = true
	f.register(t, "registry_desktop.json", map[string]any{"pid": 70003, "cwd": ws.Path, "peerProtocol": 2,
		"hostSessionId": "local_b1b2c3d4-0000-4000-8000-000000000002"})
	ctx := context.Background()

	panes, err := f.m.Panes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	since := time.UnixMilli(1792806396274)
	want := wsdomain.Pane{WorkspaceID: ws.ID, Pane: HandleOf(testUUID), State: turn.AgentWorking, StateSince: since}
	if len(panes) != 2 || !slices.Contains(panes, want) {
		t.Fatalf("Panes = %+v, want %+v among 2", panes, want)
	}

	terms, scope, err := f.m.AllTerminals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !scope.Complete || scope.RuntimeID != desktopRTID {
		t.Fatalf("scope = %+v", scope)
	}
	wantTerm := wsdomain.Terminal{Handle: HandleOf(testUUID), WorkspaceID: ws.ID, TabID: HandleOf(testUUID), Title: "repo-8a",
		Agent: "claude", Usable: true, LastOutput: since}
	if len(terms) != 2 || !slices.Contains(terms, wantTerm) {
		t.Fatalf("AllTerminals = %+v, want %+v among 2", terms, wantTerm)
	}
	for _, term := range terms {
		if term.Handle != wantTerm.Handle && term.Usable {
			t.Fatalf("a process on an unknown peer protocol is usable: %+v", term)
		}
	}

	f.exec.ps = helperPS
	if _, scope, err := f.m.AllTerminals(ctx); err != nil || scope.Complete || scope.RuntimeID != "" {
		t.Fatalf("without the app: %+v, %v", scope, err)
	}
}

func TestLiveAgentSession(t *testing.T) {
	f := newFixture(t)
	ws := f.worktree(t, "w")
	ctx := context.Background()
	if _, ok, err := f.m.LiveAgentSession(ctx, ws.ID, "claude"); ok || err != nil {
		t.Fatalf("no process yet: %v, %v", ok, err)
	}
	f.register(t, "registry_desktop.json", map[string]any{"cwd": ws.Path})
	s, ok, err := f.m.LiveAgentSession(ctx, ws.ID, "claude")
	if err != nil || !ok || s.Handle != HandleOf(testUUID) {
		t.Fatalf("LiveAgentSession = %+v, %v, %v", s, ok, err)
	}
	if _, ok, _ := f.m.LiveAgentSession(ctx, ws.ID, "codex"); ok {
		t.Fatal("found a codex session")
	}
}

func TestStartAgentSessionMintsImportsAndWaits(t *testing.T) {
	f := newFixture(t)
	ws := f.worktree(t, "w")
	mint, err := os.ReadFile(filepath.Join("testdata", "mint_output.json"))
	if err != nil {
		t.Fatal(err)
	}
	f.exec.claude = func(Cmd) ([]byte, error) { return mint, nil }
	f.exec.open = func(Cmd) error {
		f.register(t, "registry_desktop.json", map[string]any{"cwd": ws.Path})
		return nil
	}
	settings := `{"permissions":{"allow":["mcp__acme__turn_end"],"deny":["Bash(open claude://*)"]}}`
	handle, err := f.m.StartAgentSession(context.Background(), workspace.StartRequest{WorkspaceID: ws.ID, Session: testUUID,
		Agent: "claude", Model: "claude-opus-4-5", Settings: settings})
	if err != nil || handle != HandleOf(testUUID) {
		t.Fatalf("StartAgentSession = %q, %v", handle, err)
	}

	claude := f.exec.ran("claude")
	if len(claude) != 1 {
		t.Fatalf("minted %d times", len(claude))
	}
	wantArgs := []string{"-p", mintBootstrap, "--session-id", testUUID, "--setting-sources", "user", "--strict-mcp-config",
		"--max-turns", "1", "--output-format", "json", "--model", "claude-opus-4-5"}
	if c := claude[0]; !slices.Equal(c.Args, wantArgs) || c.Dir != ws.Path {
		t.Fatalf("mint = %+v", c)
	}
	if env := claude[0].Env; !slices.Equal(env, []string{"PATH=/usr/bin", "HOME=/u"}) {
		t.Fatalf("mint env = %v", env)
	}
	open := f.exec.ran("open")
	if len(open) != 1 || !slices.Equal(open[0].Args, []string{"-g", "claude://resume?session=" + testUUID}) {
		t.Fatalf("import = %+v", open)
	}
	b, err := os.ReadFile(filepath.Join(ws.Path, ".claude", "settings.local.json"))
	if err != nil || !strings.Contains(string(b), "mcp__acme__turn_end") {
		t.Fatalf("settings = %s, %v", b, err)
	}
	if status, _ := f.m.git(context.Background(), ws.Path, "status", "--porcelain"); len(status) != 0 {
		t.Fatalf("the local settings show as a change:\n%s", status)
	}

	if _, err := f.m.StartAgentSession(context.Background(), workspace.StartRequest{WorkspaceID: ws.ID, Session: testUUID}); err != nil {
		t.Fatal(err)
	}
	if n := len(f.exec.ran("claude")); n != 1 {
		t.Fatalf("a running session was minted again: %d mints", n)
	}
}

func TestStartAgentSessionTimesOut(t *testing.T) {
	f := newFixture(t)
	ws := f.worktree(t, "w")
	f.exec.claude = func(Cmd) ([]byte, error) { return []byte(`{"session_id":"` + testUUID + `"}`), nil }
	f.exec.open = func(Cmd) error { return nil }
	start := f.clk.Now()
	_, err := f.m.StartAgentSession(context.Background(), workspace.StartRequest{WorkspaceID: ws.ID, Session: testUUID})
	if err == nil || !strings.Contains(err.Error(), "within 20s") {
		t.Fatalf("err = %v", err)
	}
	if waited := f.clk.Now().Sub(start); waited < 20*time.Second || waited > 21*time.Second {
		t.Fatalf("waited %s", waited)
	}
}

func TestStartAgentSessionFailsClosedOnAnotherPeerProtocol(t *testing.T) {
	f := newFixture(t)
	ws := f.worktree(t, "w")
	f.exec.claude = func(Cmd) ([]byte, error) { return []byte(`{"session_id":"` + testUUID + `"}`), nil }
	f.exec.open = func(Cmd) error {
		f.register(t, "registry_desktop.json", map[string]any{"cwd": ws.Path, "peerProtocol": 2})
		return nil
	}
	_, err := f.m.StartAgentSession(context.Background(), workspace.StartRequest{WorkspaceID: ws.ID, Session: testUUID})
	if err == nil || !strings.Contains(err.Error(), "peer protocol 2") {
		t.Fatalf("err = %v", err)
	}
}

func TestStartAgentSessionRejectsAMintOfAnotherSession(t *testing.T) {
	f := newFixture(t)
	ws := f.worktree(t, "w")
	f.exec.claude = func(Cmd) ([]byte, error) { return []byte(`{"session_id":"` + otherUUID + `"}`), nil }
	if _, err := f.m.StartAgentSession(context.Background(), workspace.StartRequest{WorkspaceID: ws.ID, Session: testUUID}); err == nil {
		t.Fatal("accepted a mint of another session")
	}
	if len(f.exec.ran("open")) != 0 {
		t.Fatal("imported a session the mint did not make")
	}
	if _, err := f.m.StartAgentSession(context.Background(), workspace.StartRequest{WorkspaceID: ws.ID, Session: "x&y=z"}); err == nil {
		t.Fatal("accepted a session id that is no uuid")
	}
}

func TestWakeForksTheConversationAndRunsTheFork(t *testing.T) {
	f := newFixture(t)
	ws := f.worktree(t, "w")
	const next = "0b6f3e7a-1c2d-4e5f-8a9b-0c1d2e3f4a5b"
	f.m.NewUUID = func() string { return next }
	f.exec.claude = func(c Cmd) ([]byte, error) {
		return []byte(`{"session_id":"` + next + `","is_error":false,"result":"ready"}`), nil
	}
	f.exec.open = func(Cmd) error {
		f.register(t, "registry_desktop.json", map[string]any{"cwd": ws.Path, "sessionId": next, "hostSessionId": HandleOf(next)})
		return nil
	}
	handle, err := f.m.Wake(context.Background(), workspace.WakeRequest{WorkspaceID: ws.ID, Session: "loop-session", Handle: HandleOf(testUUID), Agent: "claude"})
	if err != nil || handle != HandleOf(next) {
		t.Fatalf("Wake = %q, %v", handle, err)
	}
	mint := f.exec.ran("claude")
	if len(mint) != 1 || !slices.Contains(mint[0].Args, "--fork-session") || !slices.Contains(mint[0].Args, testUUID) || !slices.Contains(mint[0].Args, next) {
		t.Fatalf("mint = %+v", mint)
	}
	open := f.exec.ran("open")
	if len(open) != 1 || !slices.Equal(open[0].Args, []string{"-g", "claude://resume?session=" + next}) {
		t.Fatalf("open = %+v", open)
	}
}

func TestAWakeWhoseForkIsLateNamesTheFork(t *testing.T) {
	f := newFixture(t)
	ws := f.worktree(t, "w")
	const next = "0b6f3e7a-1c2d-4e5f-8a9b-0c1d2e3f4a5b"
	f.m.NewUUID = func() string { return next }
	f.m.ReadyWait = time.Millisecond
	f.exec.open = func(Cmd) error { return nil }
	f.exec.claude = func(c Cmd) ([]byte, error) {
		return []byte(`{"session_id":"` + next + `","is_error":false,"result":"ready"}`), nil
	}
	handle, err := f.m.Wake(context.Background(), workspace.WakeRequest{WorkspaceID: ws.ID, Session: "loop-session", Handle: HandleOf(testUUID), Agent: "claude", Model: "opus"})
	if err == nil || handle != HandleOf(next) {
		t.Fatalf("Wake = %q, %v; want the fork's handle with an error", handle, err)
	}
	if mint := f.exec.ran("claude"); len(mint) != 1 || !slices.Contains(mint[0].Args, "opus") {
		t.Fatalf("mint = %+v", mint)
	}
}

func TestSendPromptWritesAuthThenTheMessage(t *testing.T) {
	f := newFixture(t)
	ws := f.worktree(t, "w")
	dir, err := os.MkdirTemp("", "cd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	lines := make(chan []string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			lines <- nil
			return
		}
		defer conn.Close()
		var got []string
		sc := bufio.NewScanner(conn)
		for sc.Scan() {
			got = append(got, sc.Text())
		}
		lines <- got
	}()
	f.register(t, "registry_desktop.json", map[string]any{"cwd": ws.Path, "messagingSocketPath": sock})
	f.key(t, 60919)

	ok, err := f.m.SendPrompt(context.Background(), HandleOf(testUUID), "review the PR\nplease", true)
	if err != nil || !ok {
		t.Fatalf("SendPrompt = %v, %v", ok, err)
	}
	got := <-lines
	if len(got) != 2 {
		t.Fatalf("lines = %q", got)
	}
	var auth, user map[string]any
	if json.Unmarshal([]byte(got[0]), &auth) != nil || auth["type"] != "auth" || auth["token"] != "peer-token-60919" {
		t.Fatalf("auth line = %s", got[0])
	}
	if json.Unmarshal([]byte(got[1]), &user) != nil || user["type"] != "user" {
		t.Fatalf("user line = %s", got[1])
	}
	msg := user["message"].(map[string]any)
	if msg["role"] != "user" || msg["content"] != "review the PR\nplease" {
		t.Fatalf("message = %+v", msg)
	}
}

func TestSendPromptFailsClosed(t *testing.T) {
	f := newFixture(t)
	ws := f.worktree(t, "w")
	if _, err := f.m.SendPrompt(context.Background(), HandleOf(testUUID), "x", false); err == nil {
		t.Fatal("typed into a session with no process")
	}
	f.register(t, "registry_desktop.json", map[string]any{"cwd": ws.Path, "peerProtocol": 2, "messagingSocketPath": "/nonexistent.sock"})
	f.key(t, 60919)
	_, err := f.m.SendPrompt(context.Background(), HandleOf(testUUID), "x", false)
	if err == nil || !strings.Contains(err.Error(), "peer protocol 2") {
		t.Fatalf("err = %v", err)
	}
}

func TestFocusPrefersTheLiveSessionThenTheStore(t *testing.T) {
	f := newFixture(t)
	ws := f.worktree(t, "w")
	ctx := context.Background()
	f.exec.open = func(Cmd) error { return nil }
	if err := f.m.Focus(ctx, ws.ID); err == nil {
		t.Fatal("focused a worktree Desktop has no session in")
	}
	f.store(t, "acct-1", map[string]any{"sessionId": HandleOf(otherUUID), "cwd": ws.Path, "lastActivityAt": 100})
	f.store(t, "acct-1", map[string]any{"sessionId": "local_c1b2c3d4-0000-4000-8000-000000000003", "cwd": ws.Path, "lastActivityAt": 200, "isArchived": true})
	if err := f.m.Focus(ctx, ws.ID); err != nil {
		t.Fatal(err)
	}
	f.register(t, "registry_desktop.json", map[string]any{"cwd": ws.Path})
	if err := f.m.Focus(ctx, ws.ID); err != nil {
		t.Fatal(err)
	}
	open := f.exec.ran("open")
	if len(open) != 2 || !slices.Equal(open[0].Args, []string{"claude://resume?session=" + otherUUID}) ||
		!slices.Equal(open[1].Args, []string{"claude://resume?session=" + testUUID}) {
		t.Fatalf("open = %+v", open)
	}
}

func TestClosedReadsTheStore(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if closed, err := f.m.Closed(ctx, testUUID); err != nil || !closed {
		t.Fatalf("no store file: %v, %v", closed, err)
	}
	f.store(t, "acct-1", nil)
	if closed, err := f.m.Closed(ctx, testUUID); err != nil || closed {
		t.Fatalf("open session: %v, %v", closed, err)
	}
	f.store(t, "acct-1", map[string]any{"isArchived": true})
	if closed, err := f.m.Closed(ctx, testUUID); err != nil || !closed {
		t.Fatalf("archived session: %v, %v", closed, err)
	}
	if _, err := f.m.Closed(ctx, "../x"); err == nil {
		t.Fatal("read a store path from an id that is no uuid")
	}
}

func TestWaitIdlePollsTheStatus(t *testing.T) {
	f := newFixture(t)
	ws := f.worktree(t, "w")
	ctx := context.Background()
	f.register(t, "registry_desktop.json", map[string]any{"cwd": ws.Path, "status": "busy"})
	if idle, err := f.m.WaitIdle(ctx, HandleOf(testUUID), 5*time.Second); err != nil || idle {
		t.Fatalf("busy: %v, %v", idle, err)
	}
	polls := 0
	f.m.Sleep = func(ctx context.Context, d time.Duration) error {
		if polls++; polls == 3 {
			f.register(t, "registry_desktop.json", map[string]any{"cwd": ws.Path, "status": "idle"})
		}
		return f.clk.Sleep(ctx, d)
	}
	if idle, err := f.m.WaitIdle(ctx, HandleOf(testUUID), 5*time.Second); err != nil || !idle {
		t.Fatalf("turning idle: %v, %v", idle, err)
	}
}

func TestCloseTerminalEndsTheProcess(t *testing.T) {
	f := newFixture(t)
	ws := f.worktree(t, "w")
	var killed []int
	f.m.Kill = func(pid int) error { killed = append(killed, pid); return nil }
	if err := f.m.CloseTerminal(context.Background(), HandleOf(testUUID)); err != nil || len(killed) != 0 {
		t.Fatalf("no process: %v, %v", killed, err)
	}
	f.register(t, "registry_desktop.json", map[string]any{"cwd": ws.Path})
	if err := f.m.CloseTerminal(context.Background(), HandleOf(testUUID)); err != nil || !slices.Equal(killed, []int{60919}) {
		t.Fatalf("killed %v, %v", killed, err)
	}
}
