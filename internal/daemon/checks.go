package daemon

import (
	"net/http"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// handleChecks serves the unit's failing checks as its plugin details them.
func (s *Server) handleChecks(w http.ResponseWriter, r *http.Request) {
	u, ok := s.parseUnit(r)
	if !ok {
		writeNoUnit(w, r)
		return
	}
	s.writePluginView(w, r, u, api.ViewUnitChecks, api.UnitQuery{Unit: u})
}

// writePluginView answers with view of u's plugin.
func (s *Server) writePluginView(w http.ResponseWriter, r *http.Request, u unit.Unit, view string, query any) {
	out, err := s.pluginView(r.Context(), u.Provider, view, query)
	if err != nil {
		writeActionError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(append(out, '\n'))
}
