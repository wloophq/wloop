// Package echo is a test-only agent: its sessions answer typed prompts
// with scripted records, through the same calls a real session makes.
package echo

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/record"
)

type Agent struct {
	// Start is called when a typed line names a turn with --turn.
	Start func(ctx context.Context, handle, id string) error
	// Record stores the record Reply scripts for a line.
	Record func(ctx context.Context, handle, id string, r record.Record) error
	// Reply is the record the session ends with after line; false ends the
	// line without one.
	Reply func(id, line string) (record.Record, bool)

	mu      sync.Mutex
	current map[string]string
}

var _ turns.Agent = (*Agent)(nil)

func (a *Agent) Name() string { return "echo" }

func (a *Agent) Binary() string { return "cat" }

func (a *Agent) Resume(string, string) string { return "cat" }

func (a *Agent) Command(turns.AgentSession) (string, error) { return "cat", nil }

func (a *Agent) Preflight(context.Context, turns.AgentSession) error { return nil }

// Type plays a session receiving line: a line with --turn starts that turn
// on handle, any other line continues the handle's current one.
func (a *Agent) Type(ctx context.Context, handle, line string) (bool, error) {
	id, named := turnOf(line)
	a.mu.Lock()
	if a.current == nil {
		a.current = map[string]string{}
	}
	if named {
		a.current[handle] = id
	} else {
		id = a.current[handle]
	}
	a.mu.Unlock()
	if id == "" {
		return false, fmt.Errorf("echo: session %s has no turn", handle)
	}
	if named && a.Start != nil {
		if err := a.Start(ctx, handle, id); err != nil {
			return false, err
		}
	}
	if a.Reply == nil || a.Record == nil {
		return true, nil
	}
	if r, ok := a.Reply(id, line); ok {
		if err := a.Record(ctx, handle, id, r); err != nil {
			return false, err
		}
	}
	return true, nil
}

func turnOf(line string) (string, bool) {
	f := strings.Fields(line)
	for i, w := range f {
		if w == "--turn" && i+1 < len(f) {
			return f[i+1], true
		}
	}
	return "", false
}
