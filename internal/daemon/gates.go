package daemon

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/port"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/meter"
)

// gateTickEvery is the longest the gates' tick waits between two looks.
const gateTickEvery = 30 * time.Second

// tickGates settles the tasks held by gates that closed before their
// declaration ended them, then resolves the gates whose window passed, after
// each gates note, at the earliest open deadline and at least every
// gateTickEvery, until ctx ends.
func (s *Server) tickGates(ctx context.Context) {
	if err := s.deps.Gates.SettleEnded(ctx); err != nil && ctx.Err() == nil {
		s.logf("gates: settling held tasks: %v", err)
	}
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
	q := gates.Query{Kind: gate.Kind(r.URL.Query().Get("kind"))}
	if v := r.URL.Query().Get("status"); v != "" {
		for _, st := range strings.Split(v, ",") {
			q.Statuses = append(q.Statuses, gate.Status(strings.TrimSpace(st)))
		}
	}
	ctx := withPlanReads(whereBy(r.Context(), time.Now().Add(s.whereBudget())))
	gs, err := rd.Where(ctx, q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]api.GateView, len(gs))
	byUnit := map[unit.Unit][]items.Item{}
	for i, g := range gs {
		out[i] = s.gateView(ctx, g, byUnit)
	}
	writeJSON(w, out)
}

// handleGate resolves a gate with the intent its path names. A request
// from a session resolves it as the agent through that session, which the
// gates service accepts only from a session the user drives.
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
	q, ok := s.requester(w, r)
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
		err = s.resolveDraftGateAt(ctx, g, intent, req.Args, q)
	} else {
		source := turn.Cockpit
		if q.Actor == task.ActorPerson {
			source, err = answerSource(req.Source)
		}
		if err == nil {
			err = s.deps.Gates.Resolve(messages.WithDelivery(ctx, &messages.Delivery{Source: source}), id,
				gates.Resolution{Intent: intent, Answer: req.Args["answer"], By: q.Actor, Session: q.Session})
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
	writeJSON(w, s.gateView(ctx, g, map[unit.Unit][]items.Item{}))
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
	case errors.Is(err, gates.ErrResolved), errors.Is(err, messages.ErrNotApplied):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// gateView is g as clients offer it: a draft gate with its draft as its subject and its delivery, a question,
// post or proposal gate with its message. A draft's items are read once into byUnit.
func (s *Server) gateView(ctx context.Context, g gate.Gate, byUnit map[unit.Unit][]items.Item) api.GateView {
	if o := s.deps.Outbox; o != nil && g.Kind == gate.KindDraft {
		if id, err := strconv.ParseInt(g.Subject, 10, 64); err == nil {
			if d, err := o.Store.Get(ctx, draft.ID(id)); err == nil {
				return s.draftGateView(g, s.draftDetail(ctx, d, g, byUnit))
			}
		}
	}
	v := gateView(g, s.loaded().Gates)
	if m, ok := s.messageOf(ctx, g); ok {
		v.Subject, _ = json.Marshal(s.messageView(ctx, m))
	}
	switch g.Kind {
	case gate.KindPlan:
		v.Plan = s.gatePlan(ctx, g)
	case gate.KindStep:
		v.Units = s.stepUnits(ctx, g)
	}
	return v
}

// stepUnits are the units step gate g's task holds, as their rows present them, those not ended first; none for
// a gate of no task. The task comes from the pass ctx carries, else from a read of it alone.
func (s *Server) stepUnits(ctx context.Context, g gate.Gate) []api.PlanUnit {
	if s.deps.Tasks == nil || g.Task == "" {
		return nil
	}
	var e task.Task
	if _, ok := coretasks.PassOf(ctx); ok {
		tasks, err := s.deps.Tasks.Stored(ctx, []task.ID{g.Task})
		if err != nil {
			s.logf("step gate %d: its task: %v", g.ID, err)
		}
		e = tasks[g.Task]
	} else {
		var err error
		if e, err = s.deps.Tasks.Task(ctx, g.Task); err != nil {
			s.logf("step gate %d: its task: %v", g.ID, err)
		}
	}
	shown := s.presentations(e.Units)
	var open, ended []api.PlanUnit
	for _, u := range e.Units {
		v := api.PlanUnit{Label: shown[u].Label, Title: shown[u].Title, Link: shown[u].Link}
		if s.unitEnded(ctx, u) {
			ended = append(ended, v)
		} else {
			open = append(open, v)
		}
	}
	return slices.Concat(open, ended)
}

// unitEnded says whether u has ended as its plugin says; false when nothing says.
func (s *Server) unitEnded(ctx context.Context, u unit.Unit) bool {
	if s.deps.UnitEnded == nil {
		return false
	}
	ended, err := s.deps.UnitEnded(ctx, u)
	if err != nil {
		s.logf("whether %s ended: %v", u, err)
	}
	return ended
}

// gatePlan is the pending plan item plan gate g holds, as the plan page serves it without its actions, which are
// the gate's intents; nil when the item no longer waits. A request that reads the plan once for its gates, a
// page's pass or a withPlanReads context, looks every gate up in one read of the pending items.
func (s *Server) gatePlan(ctx context.Context, g gate.Gate) *api.PlanItem {
	p := s.deps.Plan
	if p == nil || g.Subject == "" {
		return nil
	}
	v, found, err := s.pendingView(ctx, p, g.Subject)
	if err != nil {
		s.logf("plan gate %d: its item: %v", g.ID, err)
		return nil
	}
	if !found {
		return nil
	}
	return &v
}

// pendingView is the pending plan item id as the API serves it without its actions, built with every other
// pending item once per request when ctx asks for it, else on its own.
func (s *Server) pendingView(ctx context.Context, p Plan, id string) (api.PlanItem, bool, error) {
	views := func(f automation.Filter) (map[string]api.PlanItem, error) {
		its, err := p.Items(ctx, f)
		out := make(map[string]api.PlanItem, len(its))
		for _, v := range s.planViews(ctx, its, s.presentations(itemUnits(its))) {
			out[v.ID] = v
		}
		return out, err
	}
	pending := automation.Filter{Decisions: []workflow.Decision{workflow.DecisionPending}}
	var got map[string]api.PlanItem
	var err error
	if r, ok := ctx.Value(planReadsKey{}).(*planReads); ok {
		r.once.Do(func() { r.pending, r.err = views(pending) })
		got, err = r.pending, r.err
	} else if _, ok := coretasks.PassOf(ctx); ok {
		got, err = coretasks.Once(ctx, passPendingItems, func() (map[string]api.PlanItem, error) { return views(pending) })
	} else {
		pending.ID = id
		got, err = views(pending)
	}
	v, ok := got[id]
	return v, ok, err
}

const passPendingItems = "daemon.pending_items"

type planReadsKey struct{}

// planReads are the pending plan items one request read, once, as the API serves them.
type planReads struct {
	once    sync.Once
	pending map[string]api.PlanItem
	err     error
}

// withPlanReads is ctx whose plan gates read the pending plan items once for all of them.
func withPlanReads(ctx context.Context) context.Context {
	return context.WithValue(ctx, planReadsKey{}, &planReads{})
}

// draftGateView is draft gate g with dv, its draft, as its subject, and its delivery.
func (s *Server) draftGateView(g gate.Gate, dv api.Draft) api.GateView {
	v := gateView(gate.Gate{ID: g.ID, Task: g.Task, Unit: g.Unit, Kind: g.Kind, Name: g.Name, Status: g.Status, Intents: g.Intents,
		Deadline: g.Deadline, Default: g.Default, Text: g.Text}, s.loaded().Gates)
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
	v.Text = cmp.Or(g.Text, def.Label)
	for _, name := range g.Intents {
		iv := api.IntentView{ID: name, Label: name}
		for _, in := range def.Intents {
			if in.Name == name {
				iv.Label, iv.Summary, iv.Answer = in.Label, in.Summary, in.Answer
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

// openGatesOf are task id's open gates, the oldest first; in a pass, the open gates of every task are read
// once for all of them.
func (s *Server) openGatesOf(ctx context.Context, id task.ID) ([]gate.Gate, error) {
	rd := s.deps.GateReader
	if _, ok := coretasks.PassOf(ctx); ok {
		open, err := coretasks.Once(ctx, passOpenGates, func() (map[task.ID][]gate.Gate, error) {
			gs, err := rd.Where(ctx, gates.Query{Statuses: []gate.Status{gate.Open}})
			out := map[task.ID][]gate.Gate{}
			for _, g := range gs {
				out[g.Task] = append(out[g.Task], g)
			}
			return out, err
		})
		return open[id], err
	}
	gs, err := rd.OfTask(ctx, id)
	return slices.DeleteFunc(gs, func(g gate.Gate) bool { return g.Status != gate.Open }), err
}

const passOpenGates = "daemon.open_gates"

// openGates sets v's open gates.
func (ev *taskViewer) openGates(ctx context.Context, e task.Task, v *api.TaskView) error {
	rd := ev.s.deps.GateReader
	if rd == nil {
		return nil
	}
	gs, err := ev.s.openGatesOf(ctx, e.ID)
	if err != nil {
		return err
	}
	for _, g := range gs {
		if g.Status == gate.Open {
			v.Gates = append(v.Gates, ev.s.gateView(ctx, g, ev.unitItems()))
		}
	}
	return nil
}

// resolveDraftGateAt resolves draft gate g with intent as actor, under draftMu: an approval readies its draft at the
// args' rev, the draft's own when absent, with the args' text when present, and is refused while its plugin is off.
func (s *Server) resolveDraftGateAt(ctx context.Context, g gate.Gate, intent string, args map[string]string, actor requester) error {
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
