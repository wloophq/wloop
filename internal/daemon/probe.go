package daemon

import (
	"cmp"
	"context"
	"errors"
	"net/http"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// ProbeOwner and ProbeRepo name the pull request of every probe: one that
// exists nowhere, so no inventory holds it and no view has a role for it.
const (
	ProbeOwner = "loop-e2e"
	ProbeRepo  = "probe"
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
	d, ok := s.probeIntent(pr.Ref{Owner: ProbeOwner, Repo: ProbeRepo}.Unit().Provider)
	if !ok {
		writeError(w, http.StatusInternalServerError, "no intent holds the "+builtin.RoleProbe+" role")
		return
	}
	ctx := context.WithoutCancel(r.Context())
	s.deps.LoopMu.Lock()
	ref, err := s.probe(ctx, req, d)
	s.deps.LoopMu.Unlock()
	if err != nil {
		writeAgentError(w, err)
		return
	}
	b, err := s.deps.Turns.Store.Books(ctx, ref.Unit())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	book := b[ref.Unit()]
	t, ok := book.Newest(turn.Intent(d.Name))
	if !ok {
		writeError(w, http.StatusInternalServerError, "the probe stored no turn")
		return
	}
	writeJSON(w, api.ProbeResult{PR: ref.URL(), Turn: t.ID, Session: t.SentTo})
}

// probe picks a pull request number no probe used, from the clock, and
// launches the probe's turn on it.
func (s *Server) probe(ctx context.Context, req api.ProbeRequest, d dintent.Def) (pr.Ref, error) {
	ref := pr.Ref{Owner: ProbeOwner, Repo: ProbeRepo, Number: int(s.deps.Now().Unix())}
	for {
		b, err := s.deps.Turns.Store.Books(ctx, ref.Unit())
		if err != nil {
			return ref, err
		}
		if len(b[ref.Unit()].Turns) == 0 {
			break
		}
		ref.Number++
	}
	if s.deps.Turns.Launcher == nil {
		return ref, errors.New("the daemon has no launcher")
	}
	return ref, s.deps.Turns.Launcher.Launch(ctx, turns.Launch{Unit: ref.Unit(), Intent: turn.Intent(d.Name), Trigger: turn.Automated,
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
