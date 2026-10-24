package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/core/headless"
	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/turn"
)

// Headless starts headless turns; Start returns once the turn is stored, and its call runs on.
type Headless interface {
	Start(ctx context.Context, r headless.Request) (int64, error)
}

// modeRequest is what an intent that runs headless reads of an action's body; the rest of the body is the
// ordinary action request, which the session mode passes on.
type modeRequest struct {
	Mode     string   `json:"mode"` // "headless" (default) or "session"
	Items    []string `json:"items"`
	Entry    string   `json:"entry"`
	Guidance string   `json:"guidance"`
}

const actBodyMax = 64 << 10

// runModes is the one place an intent that runs headless picks its mode, whoever posts it (the generic
// action route, the GTD bridge): headless unless the body says "session", which runs the
// ordinary action path.
func (s *Server) runModes(w http.ResponseWriter, r *http.Request, d dintent.Def) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, actBodyMax))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	var req modeRequest
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
			return
		}
	}
	switch mode := req.Mode; {
	case mode == string(turn.ModeSession) && d.Runs(mode):
		r.Body = io.NopCloser(bytes.NewReader(raw))
		r.SetPathValue("action", string(d.Name))
		s.runAction(w, r)
		return
	case mode != "" && mode != string(turn.ModeHeadless), !d.Runs(string(turn.ModeHeadless)):
		writeError(w, http.StatusBadRequest, "mode is headless or session, as the intent "+string(d.Name)+"'s manifest allows")
		return
	}
	s.runHeadless(w, r, d, req)
}

func (s *Server) runHeadless(w http.ResponseWriter, r *http.Request, d dintent.Def, req modeRequest) {
	u, ok := s.parseUnit(r)
	if !ok {
		writeNoUnit(w, r)
		return
	}
	name := string(d.Name)
	if err := unitAction(s.loaded(), name, u, s.deps.Follows); err != nil {
		writeActionError(w, err)
		return
	}
	if isPullRequest(u) {
		row, _, err := s.offered(context.WithoutCancel(r.Context()), u, catalog.Asked{Name: name}, api.ActionRequest{Guidance: req.Guidance})
		if err != nil {
			writeActionError(w, err)
			return
		}
		u = row.Unit
	}
	if s.deps.Headless == nil {
		writeError(w, http.StatusServiceUnavailable, "headless turns are not wired")
		return
	}
	seq, err := s.deps.Headless.Start(r.Context(), headless.Request{Unit: u, Items: req.Items, Entry: req.Entry, Intent: turn.Intent(name),
		Guidance: req.Guidance, Trigger: turn.ByHand})
	switch {
	case errors.Is(err, headless.ErrBusy):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, headless.ErrOverCap):
		writeError(w, http.StatusTooManyRequests, err.Error())
	case errors.Is(err, headless.ErrNotHeadless):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, turns.ErrNoRestricted), errors.Is(err, turns.ErrNoAgent):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
	default:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]int64{"seq": seq})
	}
}
