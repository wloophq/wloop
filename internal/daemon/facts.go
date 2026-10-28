package daemon

import (
	"net/http"
)

func (s *Server) handleFacts(w http.ResponseWriter, r *http.Request) {
	u, err := s.parseForgeURL(r.URL.Query().Get("pr"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "pr: "+err.Error())
		return
	}
	if s.deps.Facts == nil {
		writeError(w, http.StatusServiceUnavailable, "the daemon reads no facts")
		return
	}
	f, err := s.deps.Facts(r.Context(), u)
	if err != nil {
		writeActionError(w, err)
		return
	}
	writeJSON(w, f)
}
