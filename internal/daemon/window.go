package daemon

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/wloophq/wloop/internal/api"
)

const windowBodyMax = 4 << 10

// handleWindow relays a window request to the native window's events stream.
func (s *Server) handleWindow(w http.ResponseWriter, r *http.Request) {
	var req api.WindowRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, windowBodyMax)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if !slices.Contains(api.WindowActions, req.Action) {
		writeError(w, http.StatusBadRequest, "window action "+strconv.Quote(req.Action)+" is not one of "+strings.Join(api.WindowActions, ", "))
		return
	}
	delivered := s.hub.send(api.WindowClient, api.Change{Type: "window", Window: &req})
	writeJSON(w, api.WindowResult{Delivered: delivered})
}
