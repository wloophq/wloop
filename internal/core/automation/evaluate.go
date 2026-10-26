package automation

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// Evaluate plans the workflows of trigger t, or of every trigger when t is
// empty. It reads the ledger and the facts providers only: it writes
// nothing, notes nothing, spends no budget and consumes no guidance.
func (e *Engine) Evaluate(ctx context.Context, t workflow.TriggerKind) (Plan, error) {
	ev, err := e.evaluate(ctx, t, false)
	if err != nil {
		return Plan{}, err
	}
	return ev.plan, nil
}

// planned is one item and what Sweep needs to act on it.
type planned struct {
	item         workflow.Item
	w            workflow.Workflow
	facts        workflow.Facts
	prevDecision workflow.Decision
	refusedStep  int
	refusal      string
	// guided is a pending item that runs because the user's stored guidance waits.
	guided bool
	// carried is a stored item kept as it was because it could not be
	// decided again; it does not run on the facts it was planned with.
	carried bool
	retryAt time.Time
}

type evaluation struct {
	plan      Plan
	items     []*planned
	batches   []batch
	workflows []string
	// passed are the workflow_failed events of the events held too long.
	passed []port.Event
	// decided are the fresh events this evaluation decided, marked once saved.
	decided []eventOf
}

type candidate struct {
	u   unit.Unit
	why workflow.Why
	// fresh is an event read past the cursor in this batch.
	fresh bool
	// stored is the item a non-fresh candidate was planned as.
	stored *workflow.Item
}

type planner struct {
	e       *Engine
	ctx     context.Context
	now     time.Time
	pause   Pause
	config  workflow.Facts
	facts   map[unit.Unit]workflow.Facts
	factErr map[unit.Unit]error
	marks   map[unit.Unit]map[string]string
	lists   map[unit.ProviderID][]unit.Unit
	seen    map[string]bool
	perRun  map[string]int
	perUnit map[unitOf]int
	out     []*planned
	errs    []string
	apply   bool
	passed  []port.Event
	decided []eventOf
}

type eventOf struct {
	workflow string
	id       int64
}

func (e *Engine) evaluate(ctx context.Context, t workflow.TriggerKind, apply bool) (*evaluation, error) {
	now := e.now()
	ev := &evaluation{plan: Plan{Gen: e.gen()}}
	pause, err := e.pause(ctx)
	if err != nil {
		return nil, err
	}
	var ws []workflow.Workflow
	for _, w := range e.workflows() {
		if t == "" || w.Trigger.Kind == t {
			ws = append(ws, w)
		}
	}
	slices.SortStableFunc(ws, func(a, b workflow.Workflow) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), cmp.Compare(a.Name, b.Name))
	})
	batches, errs := e.batches(ctx, ws, now, apply)
	p := &planner{e: e, ctx: ctx, now: now, pause: pause, facts: map[unit.Unit]workflow.Facts{},
		factErr: map[unit.Unit]error{}, marks: map[unit.Unit]map[string]string{}, lists: map[unit.ProviderID][]unit.Unit{}, seen: map[string]bool{},
		perRun: map[string]int{}, perUnit: map[unitOf]int{}, errs: errs, apply: apply}
	if e.Config != nil {
		p.config = e.Config()
	}
	for _, b := range batches {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if p.workflow(&b) {
			ev.batches = append(ev.batches, b)
			ev.workflows = append(ev.workflows, b.w.Name)
		}
	}
	ev.items, ev.passed, ev.decided = p.out, p.passed, p.decided
	for _, pl := range p.out {
		ev.plan.Items = append(ev.plan.Items, pl.item)
	}
	ev.plan.Errors = p.errs
	return ev, nil
}

func (p *planner) errorf(format string, args ...any) {
	p.errs = append(p.errs, fmt.Sprintf(format, args...))
}

// workflow plans one workflow's batch; false when the workflow could not be
// planned and its stored items must stay as they are.
func (p *planner) workflow(b *batch) bool {
	w := b.w
	stored, err := p.e.Ledger.Items(p.ctx, Filter{Workflow: w.Name})
	if err != nil {
		p.errorf("%s: %v", w.Name, err)
		return false
	}
	byID := make(map[string]workflow.Item, len(stored))
	for _, it := range stored {
		byID[it.ID] = it
	}
	var cands []candidate
	if w.Trigger.Kind != workflow.TriggerSweep {
		for i, it := range stored {
			why := it.Why
			why.Matched = nil
			cands = append(cands, candidate{u: it.Unit, why: why, stored: &stored[i]})
		}
	}
	base := workflow.Why{Workflow: w.Name, Trigger: w.Trigger.Kind}
	switch w.Trigger.Kind {
	case workflow.TriggerSweep:
		units, ok := p.list(w)
		if !ok {
			return false
		}
		for _, u := range units {
			cands = append(cands, candidate{u: u, why: base})
		}
	case workflow.TriggerEvent:
		for _, ev := range b.events {
			why := base
			why.Event = &ev
			cands = append(cands, candidate{u: ev.Unit, why: why, fresh: true})
		}
	case workflow.TriggerSchedule:
		if !b.fired.IsZero() {
			units, ok := p.list(w)
			if !ok {
				return false
			}
			why := base
			why.FiredAt = b.fired
			for _, u := range units {
				cands = append(cands, candidate{u: u, why: why})
			}
		}
	}
	for _, c := range cands {
		if !c.fresh {
			if err := p.decide(w, c, byID); err != nil && !errors.Is(err, ErrUnitGone) && c.stored != nil {
				p.carry(w, *c.stored)
			}
			continue
		}
		id := c.why.Event.ID
		if p.apply && p.e.events.decided(w.Name, id) {
			continue
		}
		err := p.decide(w, c, byID)
		switch {
		case err == nil:
		case errors.Is(err, ErrUnitGone):
			p.e.logf("%s: event %d skipped: unit gone", w.Name, id)
		case !p.apply:
			b.hold(id)
			continue
		case p.e.events.expired(w.Name, id, p.now):
			p.e.logf("%s: event %d passed after holding for %s: %v", w.Name, id, eventHold, err)
			p.passed = append(p.passed, port.Event{Unit: c.u, Kind: workflow.KindStepFailed,
				Key: fmt.Sprintf("%s:event:%d", w.Name, id), Data: err.Error(), At: p.now})
		default:
			b.hold(id)
			continue
		}
		p.decided = append(p.decided, eventOf{w.Name, id})
	}
	return true
}

// carry keeps a stored item the planner could not decide again, so one
// failed read does not lose an event whose cursor already moved past it.
func (p *planner) carry(w workflow.Workflow, it workflow.Item) {
	if p.seen[it.ID] {
		return
	}
	p.seen[it.ID] = true
	p.out = append(p.out, &planned{item: it, w: w, prevDecision: it.Decision, refusedStep: -1, carried: true})
}

func (p *planner) list(w workflow.Workflow) ([]unit.Unit, bool) {
	if us, ok := p.lists[w.Provider]; ok {
		return us, true
	}
	us, err := p.e.Units.List(p.ctx, w.Provider)
	if err != nil {
		p.errorf("%s: listing %s: %v", w.Name, w.Provider, err)
		return nil, false
	}
	p.lists[w.Provider] = us
	return us, true
}

func (p *planner) unitFacts(u unit.Unit) (workflow.Facts, error) {
	if f, ok := p.facts[u]; ok {
		return f, nil
	}
	if err, ok := p.factErr[u]; ok {
		return nil, err
	}
	f, err := p.e.Units.Facts(p.ctx, u)
	if err != nil {
		p.factErr[u] = err
		return nil, err
	}
	p.facts[u] = f
	return f, nil
}

func (p *planner) unitMarks(u unit.Unit) (map[string]string, error) {
	if m, ok := p.marks[u]; ok {
		return m, nil
	}
	m, err := p.e.unitMarks(p.ctx, u)
	if err != nil {
		return nil, err
	}
	p.marks[u] = m
	return m, nil
}

func (p *planner) vars(w workflow.Workflow, u unit.Unit, facts workflow.Facts, why workflow.Why) (workflow.Vars, error) {
	v := workflow.Vars{Facts: facts, Trigger: triggerFacts(why), Config: p.config, Now: p.now}
	if !readsMarks(w) {
		return v, nil
	}
	var err error
	v.Marks, err = p.unitMarks(u)
	return v, err
}

// decide applies the decision rules to one candidate; a unit that does not
// match, or whose key already ran or was dismissed, gives no item. The
// error says the candidate could not be decided: its facts, its match or
// its run could not be read.
func (p *planner) decide(w workflow.Workflow, c candidate, stored map[string]workflow.Item) error {
	facts, err := p.unitFacts(c.u)
	if err == nil {
		facts, err = p.e.scoped(p.ctx, w.Name, c.u, facts)
	}
	if err != nil {
		p.errorf("%s: %s: %v", w.Name, c.u, err)
		return err
	}
	vars, err := p.vars(w, c.u, facts, c.why)
	if err != nil {
		p.errorf("%s: %s: reading its marks: %v", w.Name, c.u, err)
		return err
	}
	if ok, err := match(w.When, vars); err != nil {
		p.errorf("%s: %s: %v", w.Name, c.u, err)
		return err
	} else if !ok {
		return nil
	}
	why := c.why
	why.Matched = matched(w.When, vars)
	key := workflow.NewKey(w, c.u, facts, why.FiredAt)
	id := key.ID()
	if p.seen[id] {
		return nil
	}
	run, ran, err := p.e.Ledger.Run(p.ctx, key)
	if err != nil {
		p.errorf("%s: %s: %v", w.Name, c.u, err)
		return err
	}
	p.seen[id] = true
	if ran && (run.Status == workflow.RunDone || run.Status == workflow.RunDismissed) {
		return nil
	}
	pl := &planned{w: w, facts: facts, refusedStep: -1, item: workflow.Item{ID: id, Key: key, Workflow: w.Name,
		Unit: c.u, Steps: slices.Clone(w.Steps), Why: why, Observe: w.Observe, Marks: marksOf(w, facts), At: p.now}}
	if ran {
		pl.item.Attempts, pl.item.Error = run.Attempts, run.Error
	}
	prev, had := stored[id]
	if had {
		pl.prevDecision = prev.Decision
	}
	p.rules(pl, vars, run, ran, prev, had)
	if had && prev.Decision == pl.item.Decision {
		pl.item.At, pl.item.Guidance = prev.At, prev.Guidance
	}
	if runsAlone(pl.item) {
		p.count(pl.item)
	}
	if g, ok := p.e.Units.(Gainer); ok {
		if gain, err := g.Gain(p.ctx, c.u); err == nil {
			pl.item.Gain = gain
		}
	}
	p.out = append(p.out, pl)
	return nil
}

// rules are the decision rules, in order: pause, limits, guards, failed
// runs, waiting, the approval window, run.
func (p *planner) rules(pl *planned, vars workflow.Vars, run workflow.Run, ran bool, prev workflow.Item, had bool) {
	w, it := pl.w, &pl.item
	decide := func(d workflow.Decision, reason string) { it.Decision, it.Reason = d, reason }
	wait := func(reason string) {
		decide(workflow.DecisionPending, reason)
		it.Default = workflow.OutcomeWait
	}
	if !w.Observe && p.paused(w, pl.facts) {
		decide(workflow.DecisionSkipped, "paused")
		return
	}
	if reason, err := p.limit(w, it.Unit); err != nil {
		decide(workflow.DecisionBlocked, err.Error())
		return
	} else if reason != "" {
		decide(workflow.DecisionSkipped, reason)
		return
	}
	rule, err := matchedRule(w.Approval, vars)
	if err != nil {
		decide(workflow.DecisionBlocked, err.Error())
		return
	}
	waitRule := rule != nil && rule.Mode == workflow.ApprovalWait
	for i, st := range w.Steps {
		refusal, err := p.e.Steps.Check(p.ctx, st, it.Unit, it.Why, CheckOpts{Observe: w.Observe})
		if err != nil {
			decide(workflow.DecisionBlocked, err.Error())
			return
		}
		if refusal != "" {
			pl.refusedStep, pl.refusal = i, refusal
			break
		}
	}
	if pl.refusal != "" && !waitRule {
		decide(workflow.DecisionBlocked, pl.refusal)
		return
	}
	if ran && run.Status == workflow.RunFailed {
		if run.Attempts >= workflow.MaxAttempts {
			decide(workflow.DecisionBlocked, fmt.Sprintf("failed %d times: %s", run.Attempts, run.Error))
			return
		}
		if retry := run.At.Add(p.e.retryBase() << run.Attempts); p.now.Before(retry) {
			decide(workflow.DecisionSkipped, "retrying after "+run.Error)
			pl.retryAt = retry
			return
		}
	}
	mode, window := w.Approval.Mode, w.Approval.Window
	if rule != nil {
		mode, window = rule.Mode, rule.Window
	}
	switch {
	case waitRule:
		wait(waitReason(rule.When, vars, pl.refusal))
		return
	case w.Observe:
		wait("observe mode")
		return
	case w.Visible():
		wait("a visible step waits for you")
		return
	case mode == workflow.ApprovalWait:
		wait("waits for you")
		return
	}
	carried := had && prev.Decision == workflow.DecisionPending && prev.Default == workflow.OutcomeRun
	if !carried && window <= 0 {
		decide(workflow.DecisionRun, "")
		return
	}
	if p.e.Guidance != nil {
		g, err := p.e.Guidance.Peek(p.ctx, it.Unit)
		if err != nil {
			p.errorf("%s: %s: %v", w.Name, it.Unit, err)
		}
		if g != "" {
			decide(workflow.DecisionRun, "guided by you")
			pl.guided = true
			return
		}
	}
	decide(workflow.DecisionPending, "runs when its window ends")
	it.Default, it.Deadline = workflow.OutcomeRun, p.now.Add(window)
	if carried {
		it.Reason, it.Deadline = prev.Reason, prev.Deadline
	}
}

// matchedRule is the first rule of a whose When holds; nil when none does.
func matchedRule(a workflow.Approval, v workflow.Vars) (*workflow.ApprovalRule, error) {
	for i := range a.Rules {
		ok, err := a.Rules[i].When.Match(v)
		if err != nil {
			return nil, fmt.Errorf("approval.rule[%d]: %w", i, err)
		}
		if ok {
			return &a.Rules[i], nil
		}
	}
	return nil, nil
}

func match(pred workflow.Predicate, v workflow.Vars) (bool, error) {
	if pred == nil {
		return true, nil
	}
	return pred.Match(v)
}

// matched are the predicate's reads with their values, sorted by name.
func matched(pred workflow.Predicate, v workflow.Vars) []workflow.Fact {
	if pred == nil {
		return nil
	}
	names := slices.Clone(pred.Reads())
	slices.Sort(names)
	names = slices.Compact(names)
	out := make([]workflow.Fact, 0, len(names))
	for _, n := range names {
		var val any
		for _, f := range []workflow.Facts{v.Facts, v.Trigger, v.Config} {
			if x, ok := f[n]; ok {
				val = x
				break
			}
		}
		out = append(out, workflow.Fact{Name: n, Value: val})
	}
	return out
}

// waitReason names what a matched wait rule waits on: its reads that hold a
// value, a pendency first, then the guard's refusal, whose backup command
// stays last.
func waitReason(pred workflow.Predicate, v workflow.Vars, refusal string) string {
	var held []string
	for _, f := range matched(pred, v) {
		if s, ok := heldValue(f.Value); ok {
			held = append(held, f.Name+" "+s)
		}
	}
	slices.SortStableFunc(held, func(a, b string) int {
		return cmp.Compare(pendencyRank(a), pendencyRank(b))
	})
	switch {
	case len(held) == 0:
		return cmp.Or(refusal, "waits for you")
	case refusal == "":
		return strings.Join(held, ", ")
	}
	return strings.Join(held, ", ") + ": " + refusal
}

func pendencyRank(read string) int {
	if name, _, _ := strings.Cut(read, " "); strings.HasSuffix(name, ".pendency") {
		return 0
	}
	return 1
}

// heldValue is v as a reason shows it, and false for a zero value.
func heldValue(v any) (string, bool) {
	switch x := v.(type) {
	case nil:
		return "", false
	case []string:
		return "[" + strings.Join(x, ", ") + "]", len(x) > 0
	case bool:
		return "true", x
	case string:
		return x, x != ""
	case time.Time:
		return x.Format(time.RFC3339), !x.IsZero()
	case time.Duration:
		return x.String(), x != 0
	case int:
		return strconv.Itoa(x), x != 0
	case int64:
		return strconv.FormatInt(x, 10), x != 0
	}
	return fmt.Sprint(v), true
}
