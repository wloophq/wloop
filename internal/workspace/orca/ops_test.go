package orca

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/wloophq/wloop/internal/domain/unit"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
	"github.com/wloophq/wloop/internal/workspace"
)

const okEmpty = `{"ok":true,"result":{}}`

// The params are the ones Orca 1.4.229's CLI handlers send for the argv each method
// used before (out/cli/handlers/{core,repo,worktree,terminal}.js).
func TestOpsParamsMatchOrcaCLI(t *testing.T) {
	replies := canned{
		"status get":      `{"ok":true,"result":{"graphStatus":"ready"}}`,
		"repo list":       `{"ok":true,"result":{"repos":[]}}`,
		"repo add":        `{"ok":true,"result":{"repo":{"id":"r","path":"/r/x","executionHostId":"ssh:box"}}}`,
		"worktree list":   `{"ok":true,"result":{"worktrees":[]}}`,
		"worktree ps":     `{"ok":true,"result":{"worktrees":[]}}`,
		"worktree set":    okEmpty,
		"terminal list":   `{"ok":true,"result":{"terminals":[]}}`,
		"terminal wait":   `{"ok":true,"result":{"wait":{"satisfied":true}}}`,
		"terminal read":   `{"ok":true,"result":{"terminal":{"source":"screen","tail":[]}}}`,
		"terminal create": `{"ok":true,"result":{"terminal":{"handle":"term_x"}}}`,
	}
	ctx := context.Background()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		run  func(m *Manager) error
		want []string
	}{
		{"Reachable", func(m *Manager) error { return m.Reachable(ctx) }, []string{`rpc status.get null`}},
		{"Repos", func(m *Manager) error { _, err := m.Repos(ctx); return err }, []string{`rpc repo.list null`}},
		{"AddRepo", func(m *Manager) error { _, err := m.AddRepo(ctx, "rel/x"); return err }, []string{`rpc repo.add {"path":` + jsonString(t, filepath.Join(cwd, "rel/x")) + `}`}},
		{"List", func(m *Manager) error { _, err := m.List(ctx); return err }, []string{`rpc worktree.list {"limit":1000}`}},
		{"AgentActivity", func(m *Manager) error { _, err := m.AgentActivity(ctx); return err }, []string{`rpc worktree.ps {"limit":1000}`}},
		{"Agents", func(m *Manager) error { _, err := m.Agents(ctx); return err }, []string{`rpc worktree.ps {"limit":1000}`}},
		{"Terminals", func(m *Manager) error { _, err := m.Terminals(ctx, "r1::/wt/a"); return err },
			[]string{`rpc terminal.list {"includeVisualLayouts":false,"worktree":"id:r1::/wt/a"}`}},
		{"LiveAgentSession", func(m *Manager) error { _, _, err := m.LiveAgentSession(ctx, "r1::/wt/a", "claude"); return err },
			[]string{`rpc terminal.list {"includeVisualLayouts":false,"worktree":"id:r1::/wt/a"}`}},
		{"SetStatus", func(m *Manager) error { return m.SetStatus(ctx, "a", "in-review") },
			[]string{`rpc worktree.set {"noParent":false,"workspaceStatus":"in-review","worktree":"id:a"}`}},
		{"SetCard", func(m *Manager) error { return m.SetCard(ctx, "a", "needs-me", "asks", true) },
			[]string{`rpc worktree.set {"comment":"asks","isUnread":true,"noParent":false,"workspaceStatus":"needs-me","worktree":"id:a"}`}},
		{"MarkUnread", func(m *Manager) error { return m.MarkUnread(ctx, "a") },
			[]string{`rpc worktree.set {"isUnread":true,"noParent":false,"worktree":"id:a"}`}},
		{"LinkUnit", func(m *Manager) error { return m.LinkUnit(ctx, "a", prUnit(42)) },
			[]string{`rpc worktree.set {"linkedPR":42,"noParent":false,"worktree":"id:a"}`}},
		{"Rename", func(m *Manager) error { return m.Rename(ctx, "a", "REVIEW x") },
			[]string{`rpc worktree.set {"displayName":"REVIEW x","noParent":false,"worktree":"id:a"}`}},
		{"WaitIdle", func(m *Manager) error { _, err := m.WaitIdle(ctx, "term_x", 15*time.Second); return err },
			[]string{`rpc terminal.wait {"for":"tui-idle","terminal":"term_x","timeoutMs":15000}`}},
		{"ReadScreen", func(m *Manager) error { _, err := m.ReadScreen(ctx, "term_x"); return err },
			[]string{`rpc terminal.read {"screen":true,"terminal":"term_x"}`}},
		{"StartAgentSession", func(m *Manager) error {
			_, err := m.StartAgentSession(ctx, workspace.StartRequest{WorkspaceID: "a", Command: "claude", Title: "review"})
			return err
		},
			[]string{"terminal create --worktree id:a --title review --command claude --json",
				`rpc terminal.wait {"for":"tui-idle","terminal":"term_x","timeoutMs":60000}`}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &recorder{canned: replies}
			if err := c.run(&Manager{R: r, RPC: r}); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(r.calls, c.want) {
				t.Fatalf("calls = %q\nwant    %q", r.calls, c.want)
			}
		})
	}
}

type scriptedRPC struct {
	raw string
	err error
}

func (s *scriptedRPC) Call(context.Context, string, any) ([]byte, error) { return []byte(s.raw), s.err }

func TestOpsFallBackOnProtocolOnly(t *testing.T) {
	list := `{"ok":true,"result":{"worktrees":[{"id":"a","repoId":"r","path":"/p"}]}}`
	cases := []struct {
		name     string
		rpc      scriptedRPC
		fallback bool
		is       error
	}{
		{"protocol", scriptedRPC{err: &RuntimeError{Kind: KindProtocol, Method: "worktree.list", Err: errors.New("mismatched response id")}}, true, nil},
		{"method not found", scriptedRPC{raw: `{"ok":false,"error":{"code":"method_not_found","message":"unknown method"}}`}, true, nil},
		{"unavailable", scriptedRPC{err: &RuntimeError{Kind: KindUnavailable, Method: "worktree.list", Err: errors.New("dial refused")}}, false, workspace.ErrUnreachable},
		{"timeout", scriptedRPC{err: &RuntimeError{Kind: KindUnavailable, Method: "worktree.list", Err: os.ErrDeadlineExceeded}}, false, os.ErrDeadlineExceeded},
		{"app error", scriptedRPC{raw: `{"ok":false,"error":{"code":"invalid_argument","message":"bad limit"}}`}, false, nil},
	}
	rpc := &scriptedRPC{}
	m := &Manager{RPC: MeteredRPC{Next: rpc}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			*rpc = c.rpc
			r := &recorder{canned: canned{"worktree list": list}}
			m.R = MeteredRunner{Next: r}
			ctx, sc := metertest.Start(t, context.Background())
			got, err := m.List(ctx)
			metertest.Within(t, sc, metertest.Budget{
				{Res: meter.Workspace, Op: "rpc worktree.list"}: 1,
				{Res: meter.Workspace, Field: metertest.Spawns}: b2i(c.fallback),
			})
			if n := metertest.Count(sc, metertest.Limit{Res: meter.Workspace, Op: "worktree list", Field: metertest.Spawns}); n != b2i(c.fallback) {
				t.Fatalf("CLI spawns = %d, want %d", n, b2i(c.fallback))
			}
			if c.fallback {
				if err != nil || len(got) != 1 || got[0].ID != "a" {
					t.Fatalf("fallback List = %+v, %v", got, err)
				}
				return
			}
			if err == nil {
				t.Fatal("no error")
			}
			if c.is != nil && !errors.Is(err, c.is) {
				t.Fatalf("err = %v, want %v", err, c.is)
			}
		})
	}
	if got := m.Fallbacks(); got != 2 {
		t.Fatalf("Fallbacks() = %d, want 2", got)
	}
}

func jsonString(t *testing.T, s string) string {
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestLinkUnitRefusesANonPositiveNumber(t *testing.T) {
	r := &recorder{canned: canned{"worktree set": okEmpty}}
	m := &Manager{R: r, RPC: r}
	for _, key := range []string{"o/r#0", "o/r#-3", "o/r", ""} {
		if err := m.LinkUnit(context.Background(), "a", unit.Unit{Provider: unit.GitHub, Key: key}); err == nil {
			t.Errorf("LinkUnit(%q) succeeded", key)
		}
	}
	if len(r.calls) != 0 {
		t.Fatalf("calls = %q", r.calls)
	}
}

func TestReadScreenRefusesAnAnswerWithoutSource(t *testing.T) {
	m := canned{"terminal read": `{"ok":true,"result":{"terminal":{"tail":["accumulated"]}}}`}.manager()
	if got, err := m.ReadScreen(context.Background(), "term_x"); err == nil || !strings.Contains(err.Error(), "screen reads") {
		t.Fatalf("ReadScreen = %q, %v", got, err)
	}
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func TestCLIOnlyOpsSpawn(t *testing.T) {
	r := &recorder{canned: canned{
		"terminal send":   `{"ok":true,"result":{"send":{"prompt":{"stages":["input_accepted","turn_started"]}}}}`,
		"terminal create": `{"ok":true,"result":{"terminal":{"handle":"term_x"}}}`,
		"terminal wait":   `{"ok":true,"result":{"wait":{"satisfied":true}}}`,
		"terminal list":   `{"ok":true,"result":{"terminals":[{"handle":"term_x","agentIdentity":"claude","connected":true,"writable":true,"lastOutputAt":1}]}}`,
		"terminal switch": okEmpty,
		"terminal close":  okEmpty,
		"worktree rm":     okEmpty,
		"worktree create": `{"ok":true,"result":{"worktree":{"id":"r1::/wt/x","path":"/wt/x"}}}`,
	}}
	m := &Manager{R: MeteredRunner{Next: r}, RPC: MeteredRPC{Next: r}}
	ctx, sc := metertest.Start(t, context.Background())
	if _, err := m.SendPrompt(ctx, "term_x", "hi", false); err != nil {
		t.Fatal(err)
	}
	if _, err := m.StartAgentSession(ctx, workspace.StartRequest{WorkspaceID: "a", Command: "claude", Title: "review"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove(ctx, "a", false); err != nil {
		t.Fatal(err)
	}
	if err := m.Focus(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if err := m.CloseTerminal(ctx, "term_x"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Create(ctx, workspace.CreateRequest{RepoID: "r1", Name: "x"}); err != nil {
		t.Fatal(err)
	}
	for _, head := range []string{"terminal send", "terminal create", "worktree rm", "terminal switch", "terminal close", "worktree create"} {
		if n := metertest.Count(sc, metertest.Limit{Res: meter.Workspace, Op: head, Field: metertest.Spawns}); n != 1 {
			t.Errorf("%s spawns = %d, want 1\n%s", head, n, metertest.Report(sc.Tally()))
		}
		if n := metertest.Count(sc, metertest.Limit{Res: meter.Workspace, Op: "rpc " + head}); n != 0 {
			t.Errorf("%s reached the runtime %d times", head, n)
		}
	}
	metertest.Within(t, sc, metertest.Budget{
		{Res: meter.Workspace, Field: metertest.Spawns}: 6,
		{Res: meter.Workspace, Op: "rpc terminal.wait"}: 1,
		{Res: meter.Workspace, Op: "rpc terminal.list"}: 1,
		{Res: meter.Workspace}:                          8,
	})
}

func TestReachableRequiresAReadyGraph(t *testing.T) {
	ctx := context.Background()
	for raw, ok := range map[string]bool{
		`{"ok":true,"result":{"graphStatus":"ready"}}`:    true,
		`{"ok":true,"result":{"graphStatus":"starting"}}`: false,
	} {
		if err := (canned{"status get": raw}).manager().Reachable(ctx); (err == nil) != ok {
			t.Errorf("runtime %s: err %v", raw, err)
		}
	}
	for raw, ok := range map[string]bool{
		`{"ok":true,"result":{"runtime":{"reachable":true,"state":"ready"}}}`:           true,
		`{"ok":true,"result":{"runtime":{"reachable":true,"state":"graph_not_ready"}}}`: false,
		`{"ok":true,"result":{"runtime":{"reachable":false,"state":"not_running"}}}`:    false,
	} {
		cli := &recorder{canned: canned{"status --json": raw}}
		if err := (&Manager{R: cli}).Reachable(ctx); (err == nil) != ok {
			t.Errorf("CLI %s: err %v", raw, err)
		}
		if !slices.Equal(cli.calls, []string{"status --json"}) {
			t.Errorf("calls = %q", cli.calls)
		}
	}
}

func prUnit(n int) unit.Unit {
	return unit.Unit{Provider: unit.GitHub, Key: fmt.Sprintf("acme/api#%d", n)}
}
