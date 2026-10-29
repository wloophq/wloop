// Package orcatest serves a fake Orca runtime on a local socket, for tests that
// run the real orca.Manager and count its calls.
package orcatest

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	wsorca "github.com/wloophq/wloop/internal/workspace/orca"
)

type Repo struct{ ID, Path, RemoteKey string }

type Agent struct {
	Pane, State, LastMessage string
	StateStartedAt           int64
}

type Worktree struct {
	ID, RepoID, Path, DisplayName, Branch, Status, Comment string
	LinkedPR                                               int
	IsMain, IsActive, Unread                               bool
	Agents                                                 []Agent
}

type Terminal struct {
	Handle, WorktreeID, TabID, Title, Agent string
	LastOutputAt                            int64
	Orphaned                                bool
}

// World is what the fake runtime lists. Truncated and OmittedHosts make its listings
// incomplete; a GraphStatus other than "" is what status.get reports instead of ready.
type World struct {
	Repos        []Repo
	Worktrees    []Worktree
	Terminals    []Terminal
	Truncated    bool
	OmittedHosts []string
	GraphStatus  string
}

// Live sizes, as measured on the user's Orca.
const (
	LiveWorktrees = 128
	LiveAgents    = 27
	LiveTerminals = 75
)

// WorktreeID is the id of Live's worktree i.
func WorktreeID(i int) string { return fmt.Sprintf("repo-%d::/wt/w-%d", i%4, i) }

// Handle is the handle of Live's terminal i, in worktree i.
func Handle(i int) string { return fmt.Sprintf("term-%d", i) }

// Live is a world of the live sizes: LiveWorktrees worktrees over four repositories,
// the first LiveAgents with a done agent in a terminal of their own, and a terminal in
// each of the first LiveTerminals.
func Live() World {
	var w World
	for r := range 4 {
		w.Repos = append(w.Repos, Repo{ID: fmt.Sprintf("repo-%d", r), Path: fmt.Sprintf("/src/repo-%d", r),
			RemoteKey: fmt.Sprintf("github.com/acme/repo-%d", r)})
	}
	for i := range LiveWorktrees {
		wt := Worktree{ID: WorktreeID(i), RepoID: fmt.Sprintf("repo-%d", i%4), Path: fmt.Sprintf("/wt/w-%d", i),
			DisplayName: fmt.Sprintf("w-%d", i), Branch: fmt.Sprintf("refs/heads/feat-%d", i), Status: "in-progress"}
		term := Terminal{Handle: Handle(i), WorktreeID: wt.ID, TabID: fmt.Sprintf("tab-%d", i), Title: "zsh", LastOutputAt: 1760745600000}
		if i < LiveAgents {
			wt.Agents = []Agent{{Pane: term.TabID + ":leaf-1", State: "done", LastMessage: "Done.", StateStartedAt: 1760745600000}}
			term.Title, term.Agent = "review", "claude"
		}
		w.Worktrees = append(w.Worktrees, wt)
		if i < LiveTerminals {
			w.Terminals = append(w.Terminals, term)
		}
	}
	return w
}

const (
	token     = "orcatest-token"
	runtimeID = "orcatest-runtime"
)

// Runtime is a fake Orca runtime: the methods the manager reads and writes over the
// socket, and `terminal send` and `terminal close` over its CLI runner.
type Runtime struct {
	dir string

	mu    sync.Mutex
	world World
}

// Start serves w until the test ends.
func Start(t testing.TB, w World) *Runtime {
	t.Helper()
	sockDir, err := os.MkdirTemp("", "orcatest")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })
	r, stop, err := Serve(t.TempDir(), filepath.Join(sockDir, "s"), w)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stop() })
	return r
}

// Serve serves w on a unix socket at sock, and writes into dir the
// orca-runtime.json that names it, as Orca does in its user data directory;
// stop closes the socket.
func Serve(dir, sock string, w World) (r *Runtime, stop func() error, err error) {
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return nil, nil, err
	}
	r = &Runtime{dir: dir, world: w}
	meta, err := json.Marshal(map[string]any{"runtimeId": runtimeID, "authToken": token,
		"transports": []map[string]string{{"kind": "unix", "endpoint": sock}}})
	if err == nil {
		err = os.WriteFile(filepath.Join(dir, "orca-runtime.json"), meta, 0o600)
	}
	if err != nil {
		ln.Close()
		return nil, nil, err
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go r.serve(conn)
		}
	}()
	return r, ln.Close, nil
}

// Manager is a metered manager on the runtime; its CLI runs only the commands the
// runtime has no method for.
func (r *Runtime) Manager() *wsorca.Manager {
	return &wsorca.Manager{R: wsorca.MeteredRunner{Next: cli{r}}, RPC: wsorca.MeteredRPC{Next: &wsorca.Runtime{UserDataPath: r.dir}},
		DefaultBranch: func(string) string { return "main" }}
}

// Update changes what the runtime lists from now on.
func (r *Runtime) Update(f func(*World)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f(&r.world)
}

func (r *Runtime) serve(conn net.Conn) {
	defer conn.Close()
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return
	}
	var req struct {
		ID     string         `json:"id"`
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
	}
	if json.Unmarshal(line, &req) != nil {
		return
	}
	result, err := r.answer(req.Method, req.Params)
	var frame map[string]any
	if err != nil {
		frame = map[string]any{"id": req.ID, "ok": false, "error": map[string]string{"code": "unsupported", "message": err.Error()},
			"_meta": map[string]string{"runtimeId": runtimeID}}
	} else {
		frame = map[string]any{"id": req.ID, "ok": true, "result": result, "_meta": map[string]string{"runtimeId": runtimeID}}
	}
	out, _ := json.Marshal(frame)
	_, _ = conn.Write(append(out, '\n'))
}

func (w *World) coverage() map[string]any {
	omitted := w.OmittedHosts
	if omitted == nil {
		omitted = []string{}
	}
	return map[string]any{"truncated": w.Truncated, "hostScope": map[string]any{"hostIds": []string{"local"}, "omittedHostIds": omitted}}
}

func (r *Runtime) answer(method string, params map[string]any) (any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	w := &r.world
	switch method {
	case "status.get":
		return map[string]any{"graphStatus": cmp.Or(w.GraphStatus, "ready"), "runtime": map[string]any{"reachable": true, "state": "ready"}}, nil
	case "repo.list":
		var rows []map[string]any
		for _, x := range w.Repos {
			rows = append(rows, map[string]any{"id": x.ID, "path": x.Path, "gitRemoteIdentity": map[string]string{"canonicalKey": x.RemoteKey}})
		}
		return map[string]any{"repos": rows}, nil
	case "worktree.list":
		var rows []map[string]any
		for _, x := range w.Worktrees {
			row := map[string]any{"id": x.ID, "repoId": x.RepoID, "path": x.Path, "displayName": x.DisplayName, "branch": x.Branch,
				"workspaceStatus": x.Status, "comment": x.Comment, "isMainWorktree": x.IsMain, "isUnread": x.Unread}
			if x.LinkedPR != 0 {
				row["linkedPR"] = x.LinkedPR
			}
			rows = append(rows, row)
		}
		return with(w.coverage(), "worktrees", rows), nil
	case "worktree.ps":
		var rows []map[string]any
		for _, x := range w.Worktrees {
			var agents []map[string]any
			for _, a := range x.Agents {
				agents = append(agents, map[string]any{"paneKey": a.Pane, "state": a.State, "stateStartedAt": a.StateStartedAt, "lastAssistantMessage": a.LastMessage})
			}
			rows = append(rows, map[string]any{"worktreeId": x.ID, "isActive": x.IsActive, "agents": agents})
		}
		return map[string]any{"worktrees": rows}, nil
	case "terminal.list":
		only, _ := params["worktree"].(string)
		var rows []map[string]any
		for _, x := range w.Terminals {
			if only != "" && "id:"+x.WorktreeID != only {
				continue
			}
			rows = append(rows, map[string]any{"handle": x.Handle, "worktreeId": x.WorktreeID, "tabId": x.TabID, "title": x.Title,
				"agentIdentity": x.Agent, "connected": !x.Orphaned, "writable": !x.Orphaned, "orphaned": x.Orphaned, "lastOutputAt": x.LastOutputAt})
		}
		return with(w.coverage(), "terminals", rows), nil
	case "worktree.set":
		id, _ := params["worktree"].(string)
		i := slices.IndexFunc(w.Worktrees, func(x Worktree) bool { return "id:"+x.ID == id })
		if i < 0 {
			return nil, fmt.Errorf("no worktree %s", id)
		}
		if s, ok := params["workspaceStatus"].(string); ok {
			w.Worktrees[i].Status = s
		}
		if s, ok := params["comment"].(string); ok {
			w.Worktrees[i].Comment = s
		}
		if n, ok := params["linkedPR"].(float64); ok {
			w.Worktrees[i].LinkedPR = int(n)
		}
		if u, ok := params["isUnread"].(bool); ok {
			w.Worktrees[i].Unread = u
		}
		return map[string]any{}, nil
	case "terminal.wait":
		return map[string]any{"wait": map[string]any{"satisfied": true}}, nil
	}
	return nil, fmt.Errorf("the fake runtime has no method %s", method)
}

func with(base map[string]any, key string, rows []map[string]any) map[string]any {
	out := map[string]any{key: rows}
	for k, v := range base {
		out[k] = v
	}
	return out
}

type cli struct{ r *Runtime }

func (c cli) Run(_ context.Context, args ...string) ([]byte, error) {
	flag := func(name string) string {
		if i := slices.Index(args, name); i >= 0 && i+1 < len(args) {
			return args[i+1]
		}
		return ""
	}
	switch strings.Join(args[:min(2, len(args))], " ") {
	case "terminal send":
		return []byte(`{"ok":true,"result":{"send":{"accepted":true,"prompt":{"stages":["input_accepted","turn_started"]}}}}`), nil
	case "terminal close":
		h := flag("--terminal")
		c.r.Update(func(w *World) {
			w.Terminals = slices.DeleteFunc(w.Terminals, func(t Terminal) bool { return t.Handle == h })
		})
		return []byte(`{"ok":true,"result":{}}`), nil
	}
	return nil, fmt.Errorf("orcatest: no CLI command %v", args)
}
