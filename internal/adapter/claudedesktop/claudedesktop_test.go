package claudedesktop_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/claudedesktop"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/workspace"
	wscd "github.com/wloophq/wloop/internal/workspace/claudedesktop"
)

const uuid = "d17d2f0e-5c1a-4b8e-9f3a-2b7c6d5e4f10"

type psOnly struct{ out string }

func (p psOnly) Run(_ context.Context, c wscd.Cmd) ([]byte, error) {
	if c.Name == "ps" {
		return []byte(p.out), nil
	}
	return nil, fmt.Errorf("unexpected %s", c)
}

type world struct {
	m        *wscd.Manager
	worktree string
}

// newWorld lays out one linked worktree under the root as git writes it, and
// one Desktop process in it.
func newWorld(t *testing.T, socket string) world {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitDir := filepath.Join(base, "api", ".git", "worktrees", "w")
	worktree := filepath.Join(base, "root", "api", "w")
	for _, d := range []string{gitDir, worktree, filepath.Join(base, "sessions")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(worktree, ".git"), "gitdir: "+gitDir+"\n")
	write(t, filepath.Join(gitDir, "commondir"), "../..\n")
	write(t, filepath.Join(gitDir, "HEAD"), "ref: refs/heads/feat/x\n")
	entry := map[string]any{"pid": 60919, "sessionId": uuid, "cwd": worktree, "startedAt": 1792806396199, "version": "2.1.308",
		"peerProtocol": 1, "kind": "interactive", "entrypoint": "claude-desktop", "hostSessionId": "local_" + uuid,
		"messagingSocketPath": socket, "name": "repo-8a", "status": "busy", "statusUpdatedAt": 1792806396274}
	b, _ := json.Marshal(entry)
	write(t, filepath.Join(base, "sessions", "60919.json"), string(b))
	write(t, filepath.Join(base, "sessions", "60919.ab12.key"), `{"peerToken":"tok"}`)
	m := wscd.New(filepath.Join(base, "root"), base)
	m.SessionsDir = filepath.Join(base, "sessions")
	m.Exec = psOnly{out: "21671 Fri Oct 23 20:56:30 2026 /Applications/Claude.app/Contents/MacOS/Claude\n"}
	m.Alive = func(int) bool { return true }
	return world{m: m, worktree: worktree}
}

func write(t *testing.T, path, s string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAdapterReadsProcessesAgentsAndCensusFromTheCache(t *testing.T) {
	w := newWorld(t, "/nonexistent.sock")
	a, err := claudedesktop.Ports(workspace.NewCached(w.m, w.m, time.Second))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if a.Name() != "claude-desktop" {
		t.Fatalf("Name = %q", a.Name())
	}
	ps, err := a.Processes(ctx, w.worktree)
	if err != nil || len(ps) != 1 || ps[0].Handle != "local_"+uuid || ps[0].Key != "local_"+uuid || ps[0].Agent != "claude" {
		t.Fatalf("Processes = %+v, %v", ps, err)
	}
	agents, err := a.Agents(ctx)
	want := turns.AgentObservation{WorkspaceID: w.worktree, Pane: "local_" + uuid, TerminalKey: "local_" + uuid,
		State: turn.AgentWorking, StateSince: time.UnixMilli(1792806396274)}
	if err != nil || len(agents) != 1 || agents[0] != want {
		t.Fatalf("Agents = %+v, %v", agents, err)
	}
	if last, ok := a.LastAgents(); !ok || len(last) != 1 {
		t.Fatalf("LastAgents = %+v, %v", last, ok)
	}
	c, err := a.Census(ctx)
	if err != nil || !c.Complete || c.RuntimeID != "21671@Fri Oct 23 20:56:30 2026" || c.Handles["local_"+uuid] != w.worktree {
		t.Fatalf("Census = %+v, %v", c, err)
	}
}

func TestAdapterRefusesAnotherManager(t *testing.T) {
	if _, err := claudedesktop.Ports(&namedManager{}); err == nil {
		t.Fatal("ran on a manager that is not Claude Desktop")
	}
}

type namedManager struct{ workspace.Manager }

func (namedManager) Name() string { return "orca" }

func TestAdapterTypesASlashCommandAsWords(t *testing.T) {
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
	got := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			got <- ""
			return
		}
		defer conn.Close()
		sc := bufio.NewScanner(conn)
		var last string
		for sc.Scan() {
			last = sc.Text()
		}
		got <- last
	}()
	w := newWorld(t, sock)
	ok, err := claudedesktop.New(w.m).Type(context.Background(), "local_"+uuid, "/acme-review https://github.com/acme/api/pull/7")
	if err != nil || !ok {
		t.Fatalf("Type = %v, %v", ok, err)
	}
	var msg struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(<-got), &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Message.Content != "Invoke the skill acme-review with the arguments: https://github.com/acme/api/pull/7" {
		t.Fatalf("typed %q", msg.Message.Content)
	}
}
