package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/internal/turn"
)

// dueEvery is how often the due job looks for pending items whose window
// ended.
const dueEvery = 5 * time.Second

var errNoPlan = errors.New("the daemon runs no workflows")

var planTriggers = []workflow.TriggerKind{workflow.TriggerSweep, workflow.TriggerEvent, workflow.TriggerSchedule}

// planActions map the actions POST /api/plan takes to the engine's; pause
// pauses the pull request, which drops its pending items.
var planActions = map[string]workflow.Action{
	"apply": workflow.ActionApply, "dismiss": workflow.ActionDismiss, "skip": workflow.ActionSkip, "guide": workflow.ActionGuide, "pause": "",
}

// planOffers are the item's action each POST action needs offered.
var planOffers = map[string]string{"apply": catalog.PlanApply, "dismiss": catalog.PlanDismiss, "skip": catalog.PlanDismiss, "guide": "guide"}

// dueJob runs the pending items whose window ended, under the loop lock. It
// runs them on the daemon's life context bounded by the job's, so their
// meter scope is the plan's own, as the gate's runs were.
func (s *Server) dueJob(p Plan) *job {
	return newJob("due", dueEvery, sweepTimeout, func(ctx context.Context) (string, string, error) {
		if due, err := s.anyDue(ctx, p); err != nil || !due {
			return "", "", err
		}
		rctx, cancel := context.WithCancel(s.life)
		defer cancel()
		defer context.AfterFunc(ctx, cancel)()
		s.deps.LoopMu.Lock()
		defer s.deps.LoopMu.Unlock()
		return "", "", p.DueRun(withLoopHeld(rctx))
	})
}

// anyDue says whether a pending item that runs by default reached its
// deadline, so the due job takes the loop lock and the round state only then.
func (s *Server) anyDue(ctx context.Context, p Plan) (bool, error) {
	items, err := p.Items(ctx, automation.Filter{Decisions: []workflow.Decision{workflow.DecisionPending}})
	if err != nil {
		return false, err
	}
	now := s.deps.Now()
	return slices.ContainsFunc(items, func(it workflow.Item) bool {
		return it.Default == workflow.OutcomeRun && !it.Deadline.After(now)
	}), nil
}

// handlePlan answers the plan: with a trigger, what evaluating it would
// decide now, which writes nothing; without one, the stored plan.
func (s *Server) handlePlan(w http.ResponseWriter, r *http.Request) {
	p := s.deps.Plan
	if p == nil {
		writeError(w, http.StatusServiceUnavailable, errNoPlan.Error())
		return
	}
	v := r.URL.Query()
	if raw := v.Get("unit"); raw != "" {
		s.handlePlanUnit(w, r, raw)
		return
	}
	q := api.PlanQuery{Workflow: v.Get("workflow"), Provider: v.Get("provider"), Trigger: v.Get("trigger")}
	ctx := r.Context()
	res := api.PlanResponse{Items: []api.PlanItem{}, Errors: []string{}}
	var items []workflow.Item
	if q.Trigger != "" {
		t := workflow.TriggerKind(q.Trigger)
		if !slices.Contains(planTriggers, t) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("trigger %q is not one of sweep, event, schedule", q.Trigger))
			return
		}
		plan, err := p.Evaluate(ctx, t)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		items = slices.DeleteFunc(plan.Items, func(it workflow.Item) bool {
			return (q.Workflow != "" && it.Workflow != q.Workflow) || (q.Provider != "" && string(it.Unit.Provider) != q.Provider)
		})
		res.Errors = append(res.Errors, plan.Errors...)
	} else {
		var err error
		if items, err = p.Items(ctx, automation.Filter{Workflow: q.Workflow, Provider: unit.ProviderID(q.Provider)}); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	res.Items = append(res.Items, s.wirePlanItems(ctx, items, s.presentations(itemUnits(items)))...)
	if q.Trigger == "" && v.Get("closed") == "1" {
		closed, err := s.closedSessions(ctx)
		if err != nil {
			res.Errors = append(res.Errors, "closed sessions: "+err.Error())
		}
		res.Closed = closed
	}
	writeJSON(w, res)
}

// closedWindow is how far back the plan's closed sessions go.
const closedWindow = 24 * time.Hour

// closedSessions are the sessions closed within closedWindow, newest first,
// each with the line that resumes it when its agent has one.
func (s *Server) closedSessions(ctx context.Context) ([]api.ClosedSession, error) {
	out := []api.ClosedSession{}
	if s.deps.Records == nil {
		return out, nil
	}
	evs, err := s.deps.Records.UnitlessEvents(ctx)
	if err != nil {
		return out, err
	}
	since := s.deps.Now().Add(-closedWindow)
	for _, ev := range slices.Backward(evs) {
		if ev.Kind != turns.KindSessionClosed || ev.At.Before(since) {
			continue
		}
		reason, resume, _ := strings.Cut(ev.Data, turns.ResumeWith)
		out = append(out, api.ClosedSession{Session: ev.Key, At: ev.At, Reason: reason, Resume: resume})
	}
	return out, nil
}

// handlePlanAct carries an action on plan items, each under the loop lock,
// and answers the items as it left them with a line per item it refused.
func (s *Server) handlePlanAct(w http.ResponseWriter, r *http.Request) {
	p := s.deps.Plan
	if p == nil {
		writeError(w, http.StatusServiceUnavailable, errNoPlan.Error())
		return
	}
	var req api.PlanActRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	req.Guidance = strings.TrimSpace(req.Guidance)
	switch _, ok := planActions[req.Action]; {
	case !ok:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("action %q is not one of apply, dismiss, skip, guide, hold", req.Action))
		return
	case len(req.IDs) == 0:
		writeError(w, http.StatusBadRequest, "no plan item to act on")
		return
	case req.Action == "guide" && req.Guidance == "":
		writeError(w, http.StatusBadRequest, "guide needs guidance")
		return
	}
	ctx := context.WithoutCancel(r.Context())
	if s.refuseUnitOf(w, func() (unit.Unit, bool) { return s.planUnit(ctx, p, req.IDs) }) {
		return
	}
	res := api.PlanResponse{Items: []api.PlanItem{}, Errors: []string{}}
	var acted []workflow.Item
	for _, id := range req.IDs {
		it, kept, err := s.actOnPlanItem(ctx, p, id, req.Action, req.Guidance)
		if err != nil {
			res.Errors = append(res.Errors, id+": "+err.Error())
		}
		if kept {
			acted = append(acted, it)
		}
	}
	res.Items = append(res.Items, s.wirePlanItems(ctx, acted, s.presentations(itemUnits(acted)))...)
	writeJSON(w, res)
}

// actOnPlanItem acts on item id when it offers the action; kept is false
// when no item is left to show.
func (s *Server) actOnPlanItem(ctx context.Context, p Plan, id, action, guidance string) (_ workflow.Item, kept bool, _ error) {
	items, err := p.Items(ctx, automation.Filter{ID: id})
	if err != nil {
		return workflow.Item{}, false, err
	}
	if len(items) == 0 {
		return workflow.Item{}, false, fmt.Errorf("no plan item %s", id)
	}
	it := items[0]
	if action == "pause" {
		if !isPullRequest(it.Unit) {
			return it, true, badRequest{"pause applies to pull requests; " + it.Unit.Label() + " is not one"}
		}
		if _, err := s.applyOverride(ctx, "pause", it.Unit, api.ActionRequest{}); err != nil {
			return it, true, err
		}
		left, err := p.Items(ctx, automation.Filter{ID: id})
		if err != nil || len(left) == 0 {
			return it, false, err
		}
		return left[0], true, nil
	}
	wire := s.wirePlanItem(ctx, it, s.presentations([]unit.Unit{it.Unit})[it.Unit])
	i := slices.IndexFunc(wire.Actions, func(st api.ActionState) bool { return st.ID == planOffers[action] })
	switch {
	case i < 0:
		return it, true, conflict{fmt.Sprintf("%s offers no %s while %s", id, action, it.Decision)}
	case !wire.Actions[i].Enabled:
		return it, true, conflict{wire.Actions[i].Reason}
	}
	s.deps.LoopMu.Lock()
	defer s.deps.LoopMu.Unlock()
	done, err := p.Act(withLoopHeld(ctx), id, planActions[action], guidance)
	if done.ID == "" {
		done = it
	}
	return done, true, err
}

// wirePlanItems are items as the API serves them, each unit presented as
// known, from presentations, presents it.
func (s *Server) wirePlanItems(ctx context.Context, items []workflow.Item, known map[unit.Unit]readmodel.Presentation) []api.PlanItem {
	out := make([]api.PlanItem, len(items))
	for i, it := range items {
		out[i] = s.wirePlanItem(ctx, it, known[it.Unit])
	}
	return out
}

// itemUnits are the units of items, in order.
func itemUnits(items []workflow.Item) []unit.Unit {
	out := make([]unit.Unit, len(items))
	for i, it := range items {
		out[i] = it.Unit
	}
	return out
}

// wirePlanItem is it as the API serves it, its unit presented as shown, with
// the actions it offers. A
// turn.start item a start block holds offers its row's trust_run and
// unblock, so only that item reads its row.
func (s *Server) wirePlanItem(ctx context.Context, it workflow.Item, shown readmodel.Presentation) api.PlanItem {
	p := api.PlanItem{ID: it.ID, Workflow: it.Workflow, Decision: string(it.Decision), Reason: it.Reason, Default: string(it.Default),
		Observe: api.ObserveMode(it.Observe), Error: it.Error, Unit: it.Unit, Label: shown.Label, Title: shown.Title, Link: shown.Link, Steps: []string{}, Deadline: it.Deadline,
		Gain: api.Gain{MemoryBytes: it.Gain.MemoryBytes, DiskBytes: it.Gain.DiskBytes}, Attempts: it.Attempts,
		Why: api.Why{Trigger: string(it.Why.Trigger), Matched: []api.Fact{}}}
	for _, st := range it.Steps {
		p.Steps = append(p.Steps, st.Use)
	}
	if ev := it.Why.Event; ev != nil {
		p.Why.Event = ev.Kind
	}
	for _, f := range it.Why.Matched {
		p.Why.Matched = append(p.Why.Matched, api.Fact{Name: f.Name, Value: turn.FactText(f.Value)})
	}
	var row []api.ActionState
	if it.Decision == workflow.DecisionBlocked && strings.HasPrefix(it.Reason, catalog.StartBlocked) &&
		slices.Contains(p.Steps, builtin.StepTurnStart) {
		if rd, ok, err := s.readRow(ctx, it.Unit); err != nil {
			s.logf("plan %s: its row: %v", it.ID, err)
		} else if ok {
			row = rd.Row.Actions
		}
	}
	p.Actions = catalog.PlanStates(s.loaded(), p, row)
	return p
}

// pendingRound is u's pending turn.start item.
func (s *Server) pendingRound(ctx context.Context, u unit.Unit) (workflow.Item, bool, error) {
	items, err := s.deps.Plan.Items(ctx, automation.Filter{Unit: &u, Decisions: []workflow.Decision{workflow.DecisionPending}})
	if err != nil {
		return workflow.Item{}, false, err
	}
	for _, it := range items {
		if slices.ContainsFunc(it.Steps, func(st workflow.Step) bool { return st.Use == builtin.StepTurnStart }) {
			return it, true, nil
		}
	}
	return workflow.Item{}, false, nil
}

// actOnRound carries a row's skip, guide or approval of u's pending round
// onto its plan item; guide with no round pending keeps the guidance for
// the next one.
func (s *Server) actOnRound(ctx context.Context, u unit.Unit, a workflow.Action, guidance string) (string, error) {
	guidance = strings.TrimSpace(guidance)
	it, ok, err := s.pendingRound(ctx, u)
	switch {
	case err != nil:
		return "", err
	case !ok && a == workflow.ActionGuide && guidance != "":
		return "no round pending; the guidance goes with the next automatic round", s.deps.Overrides.SetNextGuidance(ctx, u, guidance)
	case !ok && a == workflow.ActionApply:
		return "", conflict{"the row changed: the pending round already went out"}
	case !ok:
		return "", badRequest{"no round pending for " + u.Label() + "; add guidance to send it with the next round"}
	}
	if !LoopHeld(ctx) {
		s.deps.LoopMu.Lock()
		defer s.deps.LoopMu.Unlock()
		ctx = withLoopHeld(ctx)
	}
	if _, err := s.deps.Plan.Act(ctx, it.ID, a, guidance); err != nil {
		return "", err
	}
	if a == workflow.ActionSkip {
		return "skipped the pending " + it.Workflow, nil
	}
	return "sent the " + it.Workflow + " round", nil
}

// dropPending discards u's pending plan items without marking them handled.
func (s *Server) dropPending(ctx context.Context, u unit.Unit, why string) error {
	if s.deps.Plan == nil {
		return nil
	}
	return s.deps.Plan.Drop(ctx, u, why)
}

// dropPaused discards the pending plan items of the units pause holds:
// every unit when it is on, else the pull requests of the repositories it
// names. Observe keeps planning while paused, so a unit with an
// observe item pending is left to the next sweep, which skips its apply
// items as paused.
func (s *Server) dropPaused(ctx context.Context, p round.Pause, why string) error {
	items, err := s.deps.Plan.Items(ctx, automation.Filter{Decisions: []workflow.Decision{workflow.DecisionPending}})
	if err != nil {
		return err
	}
	var errs []error
	seen := map[unit.Unit]bool{}
	for _, it := range items {
		if it.Observe {
			seen[it.Unit] = true
		}
	}
	for _, it := range items {
		u := it.Unit
		if seen[u] {
			continue
		}
		seen[u] = true
		held := p.On
		if ref := refOf(u); ref.Repo != "" {
			held = p.Holds(ref.Owner, ref.Repo)
		}
		if held {
			errs = append(errs, s.deps.Plan.Drop(ctx, u, why))
		}
	}
	return errors.Join(errs...)
}
