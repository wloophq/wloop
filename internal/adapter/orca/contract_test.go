//go:build contract

package orca_test

import (
	"cmp"
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/orca"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/views"
	"github.com/wloophq/wloop/internal/core/views/viewstest"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/view"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/e2e"
	wsorca "github.com/wloophq/wloop/internal/workspace/orca"
)

// The suites below run on the real Orca, in the workspace named by
// LOOP_CONTRACT_WORKSPACE, which must be one the live tests made.

// unreadRunner runs orca and keeps whether the last card write of each
// workspace marked it unread: `orca worktree list` does not report it.
type unreadRunner struct {
	mu     sync.Mutex
	unread map[string]bool
}

func (u *unreadRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	out, err := wsorca.Exec{}.Run(ctx, args...)
	if err == nil && len(args) > 3 && args[0] == "worktree" && args[1] == "set" && slices.Contains(args, "--unread") {
		u.mu.Lock()
		u.unread[strings.TrimPrefix(args[3], "id:")] = true
		u.mu.Unlock()
	}
	return out, err
}

func (u *unreadRunner) reset(id string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.unread[id] = false
}

func (u *unreadRunner) get(id string) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.unread[id]
}

func contractWorkspace(t *testing.T, m *wsorca.Manager) wsdomain.Workspace {
	t.Helper()
	name := os.Getenv("LOOP_CONTRACT_WORKSPACE")
	if name == "" {
		t.Skip("set LOOP_CONTRACT_WORKSPACE to a workspace named e2e.Prefix... to run the contract on the real Orca")
	}
	t.Setenv(wsorca.LiveVar, "1")
	if err := e2e.MustOwn(name); err != nil {
		t.Fatal(err)
	}
	all, err := m.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, ws := range all {
		if ws.DisplayName == name {
			return ws
		}
	}
	t.Fatalf("Orca lists no workspace %s", name)
	return wsdomain.Workspace{}
}

func boardColumns() orca.Columns {
	cfg, err := config.Load()
	if err != nil {
		cfg = config.Defaults()
	}
	col := func(status string) string { return cfg.Column("orca", status) }
	return orca.Columns{ReviewStatus: col(config.ColumnWorking), ReviewFallbackStatus: col(config.ColumnWorkingFallback), NeedsMeStatus: col(config.ColumnNeedsMe), ReviewedStatus: col(config.ColumnReviewed), InProgressStatus: col(config.ColumnInProgress), InReviewStatus: col(config.ColumnInReview), ReadyStatus: col(config.ColumnReady), CompletedStatus: col(config.ColumnCompleted)}
}

// realBoard is the contract workspace's card. Its unmanaged column is
// LOOP_CONTRACT_PARKED, a column the loop does not manage.
type realBoard struct {
	t      *testing.T
	m      *wsorca.Manager
	u      *unreadRunner
	ws     wsdomain.Workspace
	cols   orca.Columns
	parked string
	ref    pr.Ref
}

func (b *realBoard) links(context.Context) (map[pr.Ref]orca.Link, error) {
	return map[pr.Ref]orca.Link{b.ref: {WorkspaceID: b.ws.ID, Review: true}}, nil
}

func (b *realBoard) Place(_ unit.Unit, column string) {
	args := []string{"worktree", "set", "--worktree", "id:" + b.ws.ID, "--comment", ""}
	if column != "" {
		args = append(args, "--workspace-status", column)
	}
	if _, err := b.u.Run(context.Background(), append(args, "--json")...); err != nil {
		b.t.Fatal(err)
	}
	b.u.reset(b.ws.ID)
}

func (b *realBoard) Card(unit.Unit) (string, string, bool) {
	all, err := b.m.List(context.Background())
	if err != nil {
		b.t.Fatal(err)
	}
	for _, ws := range all {
		if ws.ID == b.ws.ID {
			return ws.Status, ws.Comment, b.u.get(b.ws.ID)
		}
	}
	b.t.Fatal("the contract workspace is gone")
	return "", "", false
}

func (b *realBoard) Unmanaged() string { return b.parked }

func (b *realBoard) Column(s view.Status) string {
	switch s {
	case view.Working:
		return cmp.Or(b.cols.ReviewStatus, b.cols.ReviewFallbackStatus)
	case view.NeedsMe:
		return b.cols.NeedsMeStatus
	case view.Reviewed:
		return b.cols.ReviewedStatus
	case view.Done:
		return b.cols.CompletedStatus
	case view.InProgress:
		return b.cols.InProgressStatus
	}
	return ""
}

func TestRealSinkContract(t *testing.T) {
	u := &unreadRunner{unread: map[string]bool{}}
	m := &wsorca.Manager{R: u}
	ws := contractWorkspace(t, m)
	cols := boardColumns()
	viewstest.RunSink(t, func(t *testing.T) (views.ViewSink, views.PlacementReader, viewstest.Board) {
		b := &realBoard{t: t, m: m, u: u, ws: ws, cols: cols, parked: cmp.Or(os.Getenv("LOOP_CONTRACT_PARKED"), "parked"),
			ref: pr.Ref{Owner: "acme", Repo: "api", Number: 7}}
		a := orca.New(m, cols, b.links)
		return a, a, b
	})
}

// TestRealSessionsContract checks on the real Orca what RunSessions checks on
// its fixed World, which a real board cannot be made to show: the terminals of
// a workspace, the agents with their state, and typing into a terminal.
func TestRealSessionsContract(t *testing.T) {
	m := &wsorca.Manager{R: wsorca.Exec{}}
	ws := contractWorkspace(t, m)
	ctx := context.Background()
	title := ws.DisplayName + "-terminal"
	raw, err := wsorca.Exec{}.Run(ctx, "terminal", "create", "--worktree", "id:"+ws.ID, "--title", title, "--command", "cat", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		OK     bool `json:"ok"`
		Result struct {
			Terminal struct {
				Handle string `json:"handle"`
			} `json:"terminal"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || !created.OK || created.Result.Terminal.Handle == "" {
		t.Fatalf("creating a terminal: %s", raw)
	}
	handle := created.Result.Terminal.Handle
	t.Cleanup(func() {
		if _, err := (wsorca.Exec{}).Run(context.Background(), "terminal", "close", "--terminal", handle, "--json"); err != nil {
			t.Errorf("closing %s: %v", handle, err)
		}
	})
	a := orca.New(m, boardColumns(), func(context.Context) (map[pr.Ref]orca.Link, error) { return nil, nil })

	t.Run("processes of a workspace", func(t *testing.T) {
		ts, err := a.Processes(ctx, ws.ID)
		if err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(ts, func(x turns.Process) bool { return x.Handle == handle })
		if i < 0 || ts[i].Title != title || ts[i].Key == "" {
			t.Fatalf("processes = %+v, want %s titled %s", ts, handle, title)
		}
	})

	t.Run("agents with their pane and state", func(t *testing.T) {
		as, err := a.Agents(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, ag := range as {
			if ag.WorkspaceID == "" || ag.Pane == "" || ag.State == (turn.AgentState{}) {
				t.Errorf("agent %+v lacks its workspace, pane or state", ag)
			}
		}
	})

	t.Run("type into a terminal that runs no agent", func(t *testing.T) {
		submitted, err := a.Type(ctx, handle, brand.Name+" contract")
		if err != nil || submitted {
			t.Fatalf("submitted %v, %v: no agent turn starts in cat", submitted, err)
		}
	})

	t.Run("close removes the terminal from Terminals", func(t *testing.T) {
		raw, err := wsorca.Exec{}.Run(ctx, "terminal", "create", "--worktree", "id:"+ws.ID, "--title", title+"-close", "--command", "cat", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var c struct {
			Result struct {
				Terminal struct {
					Handle string `json:"handle"`
				} `json:"terminal"`
			} `json:"result"`
		}
		if err := json.Unmarshal(raw, &c); err != nil || c.Result.Terminal.Handle == "" {
			t.Fatalf("creating a terminal: %s", raw)
		}
		if err := a.Close(ctx, c.Result.Terminal.Handle); err != nil {
			t.Fatal(err)
		}
		ts, err := a.Processes(ctx, ws.ID)
		if err != nil {
			t.Fatal(err)
		}
		if slices.ContainsFunc(ts, func(x turns.Process) bool { return x.Handle == c.Result.Terminal.Handle }) {
			t.Fatalf("terminal %s is still listed after Close", c.Result.Terminal.Handle)
		}
	})
}

func TestRealUnregisterContract(t *testing.T) {
	name := os.Getenv("LOOP_CONTRACT_GONE_WORKSPACE")
	if name == "" {
		t.Skip("set LOOP_CONTRACT_GONE_WORKSPACE to a workspace named e2e.Prefix... whose directory was deleted")
	}
	t.Setenv(wsorca.LiveVar, "1")
	if err := e2e.MustOwn(name); err != nil {
		t.Fatal(err)
	}
	m := &wsorca.Manager{R: wsorca.Exec{}}
	ctx := context.Background()
	all, err := m.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(all, func(w wsdomain.Workspace) bool { return w.DisplayName == name })
	if i < 0 {
		t.Fatalf("Orca lists no workspace %s", name)
	}
	if _, err := os.Stat(all[i].Path); err == nil {
		t.Fatalf("%s still exists; Unregister is for a gone path", all[i].Path)
	}
	if err := m.Unregister(ctx, all[i].ID); err != nil {
		t.Fatal(err)
	}
	after, err := m.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(after, func(w wsdomain.Workspace) bool { return w.ID == all[i].ID }) {
		t.Fatalf("%s is still registered", name)
	}
}
