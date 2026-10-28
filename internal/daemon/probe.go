package daemon

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// handleProbe launches a probe turn through the launcher, which stores it,
// and answers with the turn id and the terminal it was sent to.
func (s *Server) handleProbe(w http.ResponseWriter, r *http.Request) {
	if !s.agentTurns(w) {
		return
	}
	var req api.ProbeRequest
	if !decodeAgent(w, r, &req) {
		return
	}
	if req.WorkspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id is the "+cmp.Or(s.deps.WorkspaceManager, "workspace manager's")+" workspace the probe runs in")
		return
	}
	d, ok := s.probeIntent(s.deps.Forge)
	if !ok {
		writeError(w, http.StatusInternalServerError, "no intent holds the "+builtin.RoleProbe+" role")
		return
	}
	ctx := context.WithoutCancel(r.Context())
	s.deps.LoopMu.Lock()
	u, err := s.probe(ctx, req, d)
	s.deps.LoopMu.Unlock()
	if err != nil {
		writeAgentError(w, err)
		return
	}
	b, err := s.deps.Turns.Store.Books(ctx, u)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	book := b[u]
	t, ok := book.Newest(turn.Intent(d.Name))
	if !ok {
		writeError(w, http.StatusInternalServerError, "the probe stored no turn")
		return
	}
	writeJSON(w, api.ProbeResult{PR: u.Address(), Turn: t.ID, Session: t.SentTo})
}

// probe picks a forge unit no probe used, numbered from the clock, and
// launches the probe's turn on it.
func (s *Server) probe(ctx context.Context, req api.ProbeRequest, d dintent.Def) (unit.Unit, error) {
	n := int(s.deps.Now().Unix())
	var u unit.Unit
	for {
		u = unit.Unit{Provider: s.deps.Forge, Key: fmt.Sprintf("%s/%s#%d", api.ProbeOwner, api.ProbeRepo, n)}
		b, err := s.deps.Turns.Store.Books(ctx, u)
		if err != nil {
			return u, err
		}
		if len(b[u].Turns) == 0 {
			break
		}
		n++
	}
	if s.deps.Turns.Launcher == nil {
		return u, errors.New("the daemon has no launcher")
	}
	return u, s.deps.Turns.Launcher.Launch(ctx, turns.Launch{Unit: u, Intent: turn.Intent(d.Name), Trigger: turn.Automated,
		WorkspaceID: req.WorkspaceID, Home: req.Home, Model: req.Model})
}

// probeIntent is the intent holding the probe role on units of p.
func (s *Server) probeIntent(p unit.ProviderID) (dintent.Def, bool) {
	if s.deps.Intents == nil {
		return dintent.Def{}, false
	}
	l := s.deps.Intents()
	if l == nil {
		return dintent.Def{}, false
	}
	return l.Registry.ByRole(builtin.RoleProbe, p)
}
