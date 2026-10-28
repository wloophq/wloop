package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/meter"
)

// gateTickEvery is the longest the gates' tick waits between two looks.
const gateTickEvery = 30 * time.Second

// tickGates resolves the gates whose window passed, after each gates note,
// at the earliest open deadline and at least every gateTickEvery, until ctx
// ends.
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
		wait := gateTickEvery
		if next, err := s.deps.Gates.NextDeadline(pctx); err != nil {
			s.logf("gates: next deadline: %v", err)
		} else if left := time.Until(next); left > 0 {
			wait = min(wait, left)
		}
		scope.End()
		select {
		case <-ctx.Done():
			return
		case <-poke:
		case <-s.draftClock.After(wait):
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
		out[i] = s.gateView(r.Context(), g)
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
	g, err := rd.Get(ctx, id)
	if err != nil {
		writeGateError(w, err)
		return
	}
	intent := r.PathValue("intent")
	if g.Kind == gate.KindDraft && s.deps.Outbox != nil {
		err = s.resolveDraftGateAt(ctx, g, intent, req.Args, actor)
	} else {
		source := turn.Cockpit
		if actor == task.ActorPerson {
			source, err = answerSource(req.Source)
		}
		if err == nil {
			err = s.deps.Gates.Resolve(messages.WithDelivery(ctx, &messages.Delivery{Source: source}), id, gates.Resolution{Intent: intent, Answer: req.Args["answer"], By: actor})
		}
	}
	if err != nil {
		writeGateError(w, err)
		return
	}
	if g, err = rd.Get(ctx, id); err != nil {
		writeGateError(w, err)
		return
	}
	writeJSON(w, s.gateView(ctx, g))
}

func writeGateError(w http.ResponseWriter, err error) {
	var off pluginOff
	var none noPublisher
	switch {
	case errors.As(err, &off), errors.As(err, &none), errors.Is(err, outbox.ErrConflict), errors.Is(err, outbox.ErrInvalid),
		errors.Is(err, outbox.ErrNotFound), errors.As(err, new(badRequest)):
		writeDraftError(w, err)
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

// gateView is g as clients offer it: a draft gate with its draft as its subject and its delivery, a question,
// post or proposal gate with its message.
func (s *Server) gateView(ctx context.Context, g gate.Gate) api.GateView {
	if o := s.deps.Outbox; o != nil && g.Kind == gate.KindDraft {
		if id, err := strconv.ParseInt(g.Subject, 10, 64); err == nil {
			if d, err := o.Store.Get(ctx, draft.ID(id)); err == nil {
				return s.draftGateView(g, s.draftView(ctx, d, g))
			}
		}
	}
	v := gateView(g, s.loaded().Gates)
	if m, ok := s.messageOf(ctx, g); ok {
		v.Subject, _ = json.Marshal(s.messageView(ctx, m))
	}
	return v
}

// draftGateView is draft gate g with dv, its draft, as its subject, and its delivery.
func (s *Server) draftGateView(g gate.Gate, dv api.Draft) api.GateView {
	v := gateView(gate.Gate{ID: g.ID, Task: g.Task, Unit: g.Unit, Kind: g.Kind, Name: g.Name, Status: g.Status, Intents: g.Intents,
		Deadline: g.Deadline, Default: g.Default}, s.loaded().Gates)
	v.Subject, _ = json.Marshal(dv)
	v.Delivery = dv.State
	return v
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
	for _, g := range gs {
		if g.Status == gate.Open {
			v.Gates = append(v.Gates, ev.s.gateView(ctx, g))
		}
	}
	return nil
}

// resolveDraftGateAt resolves draft gate g with intent as actor, under draftMu: an approval readies its draft at the
// args' rev, the draft's own when absent, with the args' text when present, and is refused while its plugin is off.
func (s *Server) resolveDraftGateAt(ctx context.Context, g gate.Gate, intent string, args map[string]string, actor task.Actor) error {
	s.draftMu.Lock()
	defer s.draftMu.Unlock()
	id, err := strconv.ParseInt(g.Subject, 10, 64)
	if err != nil {
		return fmt.Errorf("draft gate %d names no draft: %q", g.ID, g.Subject)
	}
	d, err := s.deps.Outbox.Store.Get(ctx, draft.ID(id))
	if err != nil {
		return err
	}
	rev := d.Rev
	if raw, ok := args["rev"]; ok {
		if rev, err = strconv.ParseInt(raw, 10, 64); err != nil || rev < 0 {
			return badRequest{fmt.Sprintf("rev is the draft's revision, not %q", raw)}
		}
	}
	var text *string
	if t, ok := args["text"]; ok {
		text = &t
	}
	if st, _ := s.intentStatus(g, intent); st == gate.Approved {
		if p := s.deps.Outbox.PublisherOf(d); !s.pluginEnabled(p) {
			return pluginOff{p}
		}
	}
	err = s.resolveDraftGate(ctx, g, intent, rev, text, actor)
	if errors.Is(err, outbox.ErrDisabled) {
		err = noPublisher{s.deps.Outbox.PublisherOf(d), err}
	}
	return err
}
