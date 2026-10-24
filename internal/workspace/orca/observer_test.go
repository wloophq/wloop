package orca

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/turn"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
	"github.com/wloophq/wloop/internal/workspace"
)

func (c canned) metered() *Manager {
	return &Manager{R: MeteredRunner{Next: c}, RPC: MeteredRPC{Next: c}}
}

func TestPanesDecodeOnce(t *testing.T) {
	ctx, sc := metertest.Start(t, context.Background())
	m := canned{"worktree ps": `{"ok":true,"result":{"worktrees":[
		{"worktreeId":"a","isActive":false,"agents":[
			{"paneKey":"t1:l1","state":"working","stateStartedAt":1760000000000,"lastAssistantMessage":"on it"},
			{"paneKey":"t1:l2","state":"waiting","stateStartedAt":1760000060000}]},
		{"worktreeId":"b","isActive":true,"agents":[]},
		{"worktreeId":"c","isActive":false,"agents":[]}
	]}}`}.metered()

	panes, err := m.Panes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	snap := &wsdomain.Snapshot{Panes: panes}
	act := snap.Activity()

	want := wsdomain.Pane{WorkspaceID: "a", Pane: "t1:l1", State: turn.AgentWorking,
		StateSince: time.UnixMilli(1760000000000), LastMessage: "on it"}
	if len(panes) != 3 || panes[0] != want || !panes[1].StateSince.Equal(time.UnixMilli(1760000060000)) {
		t.Fatalf("panes = %+v", panes)
	}
	if !act["a"].Busy || !act["a"].Waiting || len(act["a"].States) != 2 {
		t.Errorf("activity of a = %+v", act["a"])
	}
	if b := act["b"]; !b.UserActive || b.Busy || len(b.States) != 0 {
		t.Errorf("an open worktree without agents = %+v", b)
	}
	if got := metertest.Count(sc, metertest.Limit{Res: meter.Workspace, Op: "rpc worktree.ps"}); got != 1 {
		t.Errorf("worktree.ps calls = %d, want 1", got)
	}
}

func TestAgentsAndActivityLeaveTheOpenMarkOut(t *testing.T) {
	m := canned{"worktree ps": `{"ok":true,"result":{"worktrees":[
		{"worktreeId":"a","isActive":true,"agents":[]},
		{"worktreeId":"b","isActive":false,"agents":[{"paneKey":"t:l","state":"done"}]}
	]}}`}.manager()
	agents, err := m.Agents(context.Background())
	if err != nil || len(agents) != 1 || agents[0].WorkspaceID != "b" {
		t.Fatalf("agents = %+v, %v", agents, err)
	}
	act, err := m.AgentActivity(context.Background())
	if err != nil || !act["a"].UserActive || act["a"].Busy || act["b"].Busy {
		t.Fatalf("activity = %+v, %v", act, err)
	}
}

func TestAllTerminalsScope(t *testing.T) {
	const rows = `"terminals":[
		{"handle":"h1","worktreeId":"a","tabId":"t1","title":"claude","agentIdentity":"claude","connected":true,"writable":true,"lastOutputAt":1760000000000},
		{"handle":"h2","worktreeId":"b","connected":false,"writable":true}]`
	frame := func(tail string) string {
		return `{"ok":true,"result":{` + rows + `,` + tail + `},"_meta":{"runtimeId":"rt-1"}}`
	}
	ready := `{"ok":true,"result":{"graphStatus":"ready"}}`
	whole := `"truncated":false,"hostScope":{"hostIds":["local"],"omittedHostIds":[]}`
	for _, c := range []struct {
		name, list, status string
		complete           bool
	}{
		{"whole and ready", frame(whole), ready, true},
		{"truncated", frame(`"truncated":true,"hostScope":{"hostIds":["local"],"omittedHostIds":[]}`), ready, false},
		{"omitted host", frame(`"truncated":false,"hostScope":{"hostIds":["local"],"omittedHostIds":["ssh:box"]}`), ready, false},
		{"graph not ready", frame(whole), `{"ok":true,"result":{"graphStatus":"loading"}}`, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := canned{"terminal list": c.list, "status get": c.status, "status": c.status}.manager()
			terms, scope, err := m.AllTerminals(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if scope.Complete != c.complete || scope.RuntimeID != "rt-1" || !slices.Equal(scope.Hosts, []string{"local"}) {
				t.Errorf("scope = %+v, want complete %v", scope, c.complete)
			}
			want := wsdomain.Terminal{Handle: "h1", WorkspaceID: "a", TabID: "t1", Title: "claude", Agent: "claude",
				LastOutput: time.UnixMilli(1760000000000), Usable: true}
			if len(terms) != 2 || terms[0] != want || terms[1].Usable || terms[1].WorkspaceID != "b" {
				t.Errorf("terminals = %+v", terms)
			}
		})
	}
}

func TestAllTerminalsKeepsTheListWhenStatusFails(t *testing.T) {
	f := &fakeRPC{replies: map[string]string{
		"terminal.list": `{"ok":true,"result":{"terminals":[{"handle":"h1","worktreeId":"a"}],"truncated":false,"hostScope":{"hostIds":["local"],"omittedHostIds":[]}},"_meta":{"runtimeId":"r"}}`,
		"status.get":    `{"ok":false,"error":{"code":"runtime_unavailable","message":"gone"}}`,
	}}
	terms, scope, err := (&Manager{R: &countingRunner{}, RPC: f}).AllTerminals(context.Background())
	if err != nil || len(terms) != 1 || scope.Complete {
		t.Fatalf("AllTerminals = %+v, %+v, %v; want the list, incomplete", terms, scope, err)
	}
}

func TestAllTerminalsSkipsStatusOnACutShortList(t *testing.T) {
	f := &fakeRPC{replies: map[string]string{
		"terminal.list": `{"ok":true,"result":{"terminals":[],"truncated":true,"hostScope":{"hostIds":["local"],"omittedHostIds":[]}}}`,
	}}
	if _, _, err := (&Manager{R: &countingRunner{}, RPC: f}).AllTerminals(context.Background()); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(f.calls, "status.get") {
		t.Fatalf("calls = %v, want no status.get", f.calls)
	}
}

func TestListingsWithoutCoverageAreIncomplete(t *testing.T) {
	m := canned{
		"worktree list": `{"ok":true,"result":{"worktrees":[{"id":"a"}]}}`,
		"terminal list": `{"ok":true,"result":{"terminals":[],"truncated":false}}`,
		"status get":    `{"ok":true,"result":{"graphStatus":"ready"}}`,
	}.manager()
	if _, scope, err := m.Workspaces(context.Background()); err != nil || scope.Complete {
		t.Errorf("Workspaces scope = %+v, %v", scope, err)
	}
	if _, scope, err := m.AllTerminals(context.Background()); err != nil || scope.Complete {
		t.Errorf("AllTerminals scope = %+v, %v", scope, err)
	}
}

func TestAllTerminalsListsEveryWorktreeInOneCall(t *testing.T) {
	f := &fakeRPC{replies: map[string]string{
		"terminal.list": `{"ok":true,"result":{"terminals":[],"truncated":false,"hostScope":{"hostIds":["local"],"omittedHostIds":[]}},"_meta":{"runtimeId":"r"}}`,
		"status.get":    `{"ok":true,"result":{"graphStatus":"ready"}}`,
	}}
	m := &Manager{R: &countingRunner{}, RPC: f}
	if _, _, err := m.AllTerminals(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.params["terminal.list"]["worktree"]; ok {
		t.Errorf("terminal.list params = %v, want no worktree filter", f.params["terminal.list"])
	}
}

func TestReposMemoizesDefaultBranch(t *testing.T) {
	var read []string
	m := canned{"repo list": `{"ok":true,"result":{"repos":[
		{"id":"r","path":"/src/api","executionHostId":"local"},
		{"id":"s","path":"/src/web"},
		{"id":"x","path":"/remote/api","executionHostId":"ssh:box"},
		{"id":"n","path":"/src/fresh"}
	]}}`}.manager()
	m.DefaultBranch = func(path string) string {
		read = append(read, path)
		if path == "/src/fresh" {
			return ""
		}
		return "main"
	}
	for range 2 {
		repos, err := m.Repos(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if repos[0].DefaultBranch != "main" || repos[2].DefaultBranch != "" {
			t.Fatalf("repos = %+v", repos)
		}
	}
	if !slices.Equal(read, []string{"/src/api", "/src/web", "/src/fresh", "/src/fresh"}) {
		t.Fatalf("origin/HEAD read for %v, want a known branch once and an unknown one on each load", read)
	}
}

func TestWorkspaceUnreadFromList(t *testing.T) {
	m := canned{"worktree list": `{"ok":true,"result":{"worktrees":[
		{"id":"a","isUnread":true},{"id":"b"}
	],"truncated":false,"hostScope":{"hostIds":["local"],"omittedHostIds":[]}},"_meta":{"runtimeId":"rt"}}`}.manager()
	ws, scope, err := m.Workspaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !ws[0].Unread || ws[1].Unread {
		t.Errorf("unread = %v, %v", ws[0].Unread, ws[1].Unread)
	}
	if !scope.Complete || scope.RuntimeID != "rt" {
		t.Errorf("scope = %+v", scope)
	}
}

func TestExecRefusesTheRealOrcaInTests(t *testing.T) {
	t.Setenv(LiveVar, "")
	m := New()
	m.RPC = nil
	err := m.Remove(context.Background(), "ws-1", false)
	if !errors.Is(err, workspace.ErrUnreachable) || !strings.Contains(err.Error(), LiveVar) {
		t.Fatalf("Remove on New() = %v", err)
	}
	if _, err := (Exec{Bin: "true"}).Run(context.Background(), "status"); err != nil {
		t.Fatalf("a stand-in binary was refused: %v", err)
	}
}
