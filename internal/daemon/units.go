package daemon

import (
	"context"
	"net/http"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// Skips are the workflows the user stopped on one unit; *skips.Service satisfies it.
type Skips interface {
	Skip(ctx context.Context, workflow string, u unit.Unit) error
	Unskip(ctx context.Context, workflow string, u unit.Unit) error
	Of(ctx context.Context, u unit.Unit) ([]string, error)
}

func (s *Server) handleUnitSkip(w http.ResponseWriter, r *http.Request) { s.setSkip(w, r, true) }

func (s *Server) handleUnitUnskip(w http.ResponseWriter, r *http.Request) { s.setSkip(w, r, false) }

// setSkip stops a loaded workflow on the route's unit, withdrawing its drafts held there, or lets it run again.
func (s *Server) setSkip(w http.ResponseWriter, r *http.Request, skip bool) {
	u, ok := s.parseUnit(r)
	if !ok {
		writeNoUnit(w, r)
		return
	}
	if _, ok := s.forUser(w, r, "skipping a workflow on a conversation"); !ok {
		return
	}
	name := r.PathValue("workflow")
	if !s.workflowLoaded(name) {
		writeError(w, http.StatusNotFound, s.notice(builtin.NoticeWorkflowUnknown, map[string]any{"Workflow": name}))
		return
	}
	if s.deps.Skips == nil {
		writeError(w, http.StatusServiceUnavailable, "this daemon keeps no skips")
		return
	}
	set := s.deps.Skips.Unskip
	if skip {
		set = s.deps.Skips.Skip
	}
	if err := set(r.Context(), name, u); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, api.UnitSkipState{Unit: string(u.Provider) + "/" + u.Key, Workflow: name, Skipped: skip})
}

// skippedOn are the workflows skipped on u, empty for none or when they do not read.
func (s *Server) skippedOn(ctx context.Context, u unit.Unit) []string {
	if s.deps.Skips == nil {
		return []string{}
	}
	ws, err := s.deps.Skips.Of(ctx, u)
	if err != nil {
		s.logf("skips of %s: %v", u.Label(), err)
	}
	if ws == nil {
		ws = []string{}
	}
	return ws
}
