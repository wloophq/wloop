package daemon

import (
	"encoding/json"
	"net/http"

	"github.com/wloophq/wloop/internal/api"
)

func (s *Server) handlePluginAction(w http.ResponseWriter, r *http.Request) {
	if s.deps.PluginAction == nil {
		writeError(w, http.StatusNotFound, "no plugin runs actions in this daemon")
		return
	}
	var req api.PluginActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "plugin action: "+err.Error())
		return
	}
	req.Action = r.PathValue("action")
	out, err := s.deps.PluginAction(r.Context(), r.PathValue("id"), req)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out)
}
