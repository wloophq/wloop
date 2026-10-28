package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/meter"
)

// gateTickEvery bounds how late a gate whose window passed resolves.
const gateTickEvery = 30 * time.Second

// tickGates resolves the gates whose window passed, after each gates note
// and every gateTickEvery, until ctx ends.
func (s *Server) tickGates(ctx context.Context) {
	var poke <-chan struct{}
	if s.deps.Changes != nil {
		p, cancel := change.Poke(s.deps.Changes, port.TopicGates)
		defer cancel()
		poke = p
	}
	for {
		pctx, scope := meter.Start(ctx, meter.Loop("gates"))
		if err := s.deps.Gates.Tick(pctx, time.Now()); err != nil && ctx.Err() == nil {
			s.logf("gates: tick: %v", err)
		}
		scope.End()
		select {
		case <-ctx.Done():
			return
		case <-poke:
		case <-s.draftClock.After(gateTickEvery):
		}
	}
}

func (s *Server) gateReader(w http.ResponseWriter) (gates.Reader, bool) {
	if s.deps.Gates == nil || s.deps.GateReader == nil {
		writeError(w, http.StatusServiceUnavailable, "gates are not served")
		return nil, false
	}
	return s.deps.GateReader, true
}

func (s *Server) handleGates(w http.ResponseWriter, r *http.Request) {
	rd, ok := s.gateReader(w)
	if !ok {
		return
	}
	var q gates.Query
	if v := r.URL.Query().Get("status"); v != "" {
		for _, st := range strings.Split(v, ",") {
			q.Statuses = append(q.Statuses, gate.Status(strings.TrimSpace(st)))
		}
	}
	gs, err := rd.Where(r.Context(), q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]api.GateView, len(gs))
	for i, g := range gs {
		out[i] = gateView(g, s.loaded().Gates)
	}
	writeJSON(w, out)
}

// handleGate resolves a gate with the intent its path names. A request
// from a session resolves it as the agent, which the gates service refuses.
func (s *Server) handleGate(w http.ResponseWriter, r *http.Request) {
	rd, ok := s.gateReader(w)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid gate id: "+r.PathValue("id"))
		return
	}
	var req api.TaskRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, tasksBodyMax)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	actor, ok := requestActor(w, r)
	if !ok {
		return
	}
	ctx := context.WithoutCancel(r.Context())
	res := gates.Resolution{Intent: r.PathValue("intent"), Answer: req.Args["answer"], By: actor}
	if err := s.deps.Gates.Resolve(ctx, id, res); err != nil {
		writeGateError(w, err)
		return
	}
	g, err := rd.Get(ctx, id)
	if err != nil {
		writeGateError(w, err)
		return
	}
	writeJSON(w, gateView(g, s.loaded().Gates))
}

func writeGateError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, gates.ErrRefused):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, gates.ErrIntent):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, gates.ErrNoGate):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, gates.ErrResolved):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// gateView is g as clients offer it, worded by gates.toml.
func gateView(g gate.Gate, d intentload.GatesDef) api.GateView {
	v := api.GateView{ID: g.ID, Task: string(g.Task), Kind: string(g.Kind), Name: g.Name, Status: string(g.Status),
		Deadline: g.Deadline, Default: g.Default, Intents: []api.IntentView{}}
	if !g.Unit.IsZero() {
		v.Unit = g.Unit.String()
	}
	def, _ := d.Gate(g.Name)
	v.Text = def.Label
	for _, name := range g.Intents {
		iv := api.IntentView{ID: name, Label: name}
		for _, in := range def.Intents {
			if in.Name == name {
				iv.Label, iv.Answer = in.Label, in.Answer
			}
		}
		v.Intents = append(v.Intents, iv)
	}
	if g.Subject != "" {
		if json.Valid([]byte(g.Subject)) {
			v.Subject = json.RawMessage(g.Subject)
		} else {
			v.Subject, _ = json.Marshal(g.Subject)
		}
	}
	return v
}

// openGates sets v's open gates.
func (ev *taskViewer) openGates(ctx context.Context, e task.Task, v *api.TaskView) error {
	rd := ev.s.deps.GateReader
	if rd == nil {
		return nil
	}
	gs, err := rd.OfTask(ctx, e.ID)
	if err != nil {
		return err
	}
	d := ev.s.loaded().Gates
	for _, g := range gs {
		if g.Status == gate.Open {
			v.Gates = append(v.Gates, gateView(g, d))
		}
	}
	return nil
}
