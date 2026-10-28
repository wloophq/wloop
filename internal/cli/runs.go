package cli

import (
	"cmp"
	"context"
	"fmt"

	"github.com/wloophq/wloop/internal/daemon"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/workspace"
)

// runHost reaches the workspaces and sessions of a task's runs through the
// enabled workspace managers.
type runHost struct{ e *Env }

var _ daemon.RunHost = runHost{}

func (h runHost) Focus(ctx context.Context, manager, workspaceID string) error {
	return h.e.WS.Focus(ctx, wsdomain.Workspace{Manager: manager, ID: workspaceID})
}

func (h runHost) Wake(ctx context.Context, u unit.Unit, sess dturn.Session) error {
	cfg, err := h.e.loadConfig()
	if err != nil {
		return err
	}
	cfg.AgentHome = cmp.Or(sess.Home, cfg.AgentHome)
	ws := wsdomain.Workspace{Manager: sess.Manager, ID: sess.WorkspaceID, Path: sess.Worktree}
	m, err := h.e.WS.For(ctx, ws)
	if err != nil {
		return err
	}
	w, ok := workspace.Capability[workspace.Waker](m)
	if !ok {
		return fmt.Errorf("%s cannot wake the session %s", h.e.displayOf([]string{m.Name()}), sess.ID)
	}
	_, woke, err := h.e.wakeSession(ctx, cfg, m, w, ws, u, roundOptions{}, sess)
	if err == nil && !woke {
		err = fmt.Errorf("waking the session %s failed", sess.ID)
	}
	return err
}
