package cli

import (
	"cmp"
	"context"
	"fmt"

	"github.com/wloophq/wloop/internal/config"
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

func (h runHost) Wake(ctx context.Context, u unit.Unit, intent string, sess dturn.Session) error {
	cfg, err := h.e.loadConfig()
	if err != nil {
		return err
	}
	opts, err := h.wakeOptions(ctx, cfg, u, intent)
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
	_, woke, err := h.e.wakeSession(ctx, cfg, m, w, ws, u, opts, sess)
	if err == nil && !woke {
		err = fmt.Errorf("waking the session %s failed", sess.ID)
	}
	return err
}

// wakeOptions are the round options a turn of intent on u launches its
// session with: the manifest's policy and session name, and for a pull
// request its trust and project-settings rule.
func (h runHost) wakeOptions(ctx context.Context, cfg config.Config, u unit.Unit, intent string) (roundOptions, error) {
	d, err := h.e.intentDef(intent)
	if err != nil {
		return roundOptions{}, err
	}
	opts := intentOptions(d, roundOptions{allowUserActive: true, unit: u})
	opts.names = h.e.nameFacts(ctx, u)
	if isPR(u) {
		p, err := h.e.basics(ctx, prOf(u))
		if err != nil {
			return roundOptions{}, err
		}
		opts.pr, opts.trusted = p, trusts(cfg, p)
	}
	return opts, nil
}
