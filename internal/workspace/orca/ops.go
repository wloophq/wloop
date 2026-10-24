package orca

import (
	"context"
	"errors"
	"fmt"
	"strconv"
)

// op is one Orca call: the runtime method and params, and the CLI argv that does the
// same. A method "" means CLI only; a nil argv means runtime only.
type op struct {
	method string
	params map[string]any
	argv   []string
}

func cliOp(argv ...string) op { return op{argv: argv} }

// do runs o over the runtime and falls back to the CLI only when the runtime answers
// ErrProtocol or the envelope error code "method_not_found"; Fallbacks counts those.
// Without an RPC every op goes to the CLI.
func (m *Manager) do(ctx context.Context, out any, o op) error {
	_, err := m.doMeta(ctx, out, o)
	return err
}

// doMeta is do that also returns the runtime id the answer carried.
func (m *Manager) doMeta(ctx context.Context, out any, o op) (string, error) {
	if o.method == "" || m.RPC == nil {
		if o.argv == nil {
			return "", fmt.Errorf("orca rpc %s: no runtime client", o.method)
		}
		return m.call(ctx, out, o.argv...)
	}
	runtimeID, err := m.rpc(ctx, out, o.method, o.params)
	if !drifted(err) || o.argv == nil {
		return runtimeID, err
	}
	m.fallbacks.Add(1)
	return m.call(ctx, out, o.argv...)
}

// Fallbacks is how many calls this process sent to the CLI because the runtime drifted.
func (m *Manager) Fallbacks() int64 { return m.fallbacks.Load() }

func drifted(err error) bool {
	var oe *orcaError
	return errors.Is(err, ErrProtocol) || errors.As(err, &oe) && oe.code == "method_not_found"
}

func (m *Manager) call(ctx context.Context, out any, args ...string) (string, error) {
	args = append(args, "--json")
	raw, err := m.R.Run(ctx, args...)
	if err != nil {
		return "", err
	}
	return decode(raw, out, args)
}

func (m *Manager) rpc(ctx context.Context, out any, method string, params map[string]any) (string, error) {
	var p any
	if params != nil {
		p = params
	}
	raw, err := m.RPC.Call(ctx, method, p)
	if err != nil {
		return "", err
	}
	return decode(raw, out, []string{"rpc", method})
}

func statusOp() op { return op{method: "status.get", argv: []string{"status"}} }

func repoListOp() op { return op{method: "repo.list", argv: []string{"repo", "list"}} }

func repoAddOp(path, abs string) op {
	return op{method: "repo.add", params: map[string]any{"path": abs}, argv: []string{"repo", "add", "--path", path}}
}

func worktreeListOp() op {
	return op{method: "worktree.list", params: map[string]any{"limit": 1000}, argv: []string{"worktree", "list", "--limit", "1000"}}
}

func worktreePsOp() op {
	return op{method: "worktree.ps", params: map[string]any{"limit": 1000}, argv: []string{"worktree", "ps", "--limit", "1000"}}
}

func allTerminalsOp() op {
	return op{method: "terminal.list", params: map[string]any{"includeVisualLayouts": false}, argv: []string{"terminal", "list"}}
}

func terminalListOp(workspaceID string) op {
	return op{method: "terminal.list",
		params: map[string]any{"worktree": "id:" + workspaceID, "includeVisualLayouts": false},
		argv:   []string{"terminal", "list", "--worktree", "id:" + workspaceID}}
}

func terminalWaitOp(handle string, timeoutMs int64) op {
	return op{method: "terminal.wait",
		params: map[string]any{"terminal": handle, "for": "tui-idle", "timeoutMs": timeoutMs},
		argv:   []string{"terminal", "wait", "--terminal", handle, "--for", "tui-idle", "--timeout-ms", strconv.FormatInt(timeoutMs, 10)}}
}

func terminalReadOp(handle string) op {
	return op{method: "terminal.read", params: map[string]any{"terminal": handle, "screen": true},
		argv: []string{"terminal", "read", "--terminal", handle, "--screen"}}
}

// card is what one worktree.set changes; a zero field leaves that part as it is.
type card struct {
	status, comment, displayName string
	unread                       bool
	linkedPR                     int
}

func (c card) empty() bool { return c == card{} }

func worktreeSetOp(id string, c card) op {
	params := map[string]any{"worktree": "id:" + id, "noParent": false}
	argv := []string{"worktree", "set", "--worktree", "id:" + id}
	if c.displayName != "" {
		params["displayName"] = c.displayName
		argv = append(argv, "--display-name", c.displayName)
	}
	if c.linkedPR != 0 {
		params["linkedPR"] = c.linkedPR
		argv = append(argv, "--pr", strconv.Itoa(c.linkedPR))
	}
	if c.status != "" {
		params["workspaceStatus"] = c.status
		argv = append(argv, "--workspace-status", c.status)
	}
	if c.comment != "" {
		params["comment"] = c.comment
		argv = append(argv, "--comment", c.comment)
	}
	if c.unread {
		params["isUnread"] = true
		argv = append(argv, "--unread")
	}
	return op{method: "worktree.set", params: params, argv: argv}
}
