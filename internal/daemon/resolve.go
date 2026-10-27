package daemon

import (
	"net/http"

	"github.com/wloophq/wloop/internal/api"
)

func (s *Server) handleResolve(w http.ResponseWriter, r *http.Request) {
	address := r.URL.Query().Get("address")
	if address == "" {
		writeError(w, http.StatusBadRequest, "address is required")
		return
	}
	if s.deps.Resolve == nil {
		writeJSON(w, api.Resolved{})
		return
	}
	u, ok, err := s.deps.Resolve(r.Context(), address)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, api.Resolved{Unit: u, Found: ok})
}
