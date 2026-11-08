package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/automation"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
)

// itemsPlan is a plan whose stored items are its own, which nothing acts on; reads counts its reads.
type itemsPlan struct {
	items []workflow.Item
	reads atomic.Int32
}

func (p *itemsPlan) Evaluate(context.Context, workflow.TriggerKind) (automation.Plan, error) {
	return automation.Plan{}, nil
}

func (p *itemsPlan) Items(_ context.Context, f automation.Filter) ([]workflow.Item, error) {
	p.reads.Add(1)
	var out []workflow.Item
	for _, it := range p.items {
		if (f.ID == "" || it.ID == f.ID) && (len(f.Decisions) == 0 || slices.Contains(f.Decisions, it.Decision)) {
			out = append(out, it)
		}
	}
	return out, nil
}

func (p *itemsPlan) Act(context.Context, string, workflow.Action, string) (workflow.Item, error) {
	return workflow.Item{}, nil
}
func (p *itemsPlan) Drop(context.Context, unit.Unit, string) error { return nil }
func (p *itemsPlan) Release(context.Context, gate.Gate) error      { return nil }

const doneReacted = "slack-done-reacted"

// readItems is the items ledger counting its reads: by unit and by batch.
type readItems struct {
	l            *coreitems.Ledger
	byUnit, load atomic.Int32
}

func (c *readItems) Items(ctx context.Context, u unit.Unit) ([]items.Item, error) {
	c.byUnit.Add(1)
	return c.l.Items(ctx, u)
}

func (c *readItems) Load(ctx context.Context, us []unit.Unit) (map[unit.Unit][]items.Item, error) {
	c.load.Add(1)
	return c.l.Load(ctx, us)
}

// threadOf is the conversation task id was derived from.
func threadOf(id task.ID) unit.Unit { return unit.Unit{Provider: "slack", Key: "T1/C1/" + string(id)} }

// putDerived stores task id, derived from its thread's items: two open, which it holds, and one resolved.
func putDerived(t testing.TB, r *tasksRig, id task.ID, title string) {
	t.Helper()
	th := threadOf(id)
	at := unit.Anchor{Unit: th, Ref: "user:1"}
	var ops []items.Op
	for _, it := range []struct{ id, summary string }{{"i1", "Ana asks for the retry numbers"}, {"i2", "Bea asks when it ships"}, {"i3", "Cid thanked"}} {
		ops = append(ops, items.Op{Op: items.OpCreate, Unit: th, Item: it.id, Anchor: at, Fields: &items.Fields{Kind: ptrOf(items.Todo), Summary: ptrOf(it.summary)}})
	}
	ops = append(ops, items.Op{Op: items.OpResolve, Unit: th, Item: "i3", Anchor: at, Resolution: items.Done})
	if _, err := r.ledger.Apply(context.Background(), items.User, ops); err != nil {
		t.Fatal(err)
	}
	r.put(t, task.Task{ID: id, State: "next", Category: task.CategoryReady, Title: title, Origin: task.OriginDerived, Units: []unit.Unit{th},
		Items: []task.ItemRef{{Unit: th, Item: "i1"}, {Unit: th, Item: "i2"}, {Unit: th, Item: "i3"}}})
}

// planGateRig is a pt-BR daemon with task t-0000000000000001, derived from a thread's items, held by the open
// plan gate of a slack-done-reacted item on it, which resolves its items as moot.
func planGateRig(t testing.TB) (*tasksRig, *intentload.Loaded, gate.Gate) {
	t.Helper()
	pt := loadedIn(t, "pt-BR")
	id := task.ID("t-0000000000000001")
	u := task.UnitOf(id)
	plan := &itemsPlan{items: []workflow.Item{{ID: doneReacted + "/task/t-0000000000000001/28f2c473", Workflow: doneReacted, Unit: u,
		Decision: workflow.DecisionPending, Steps: []workflow.Step{{Use: "items.resolve", Reach: workflow.ReachLocal, Args: map[string]any{"resolution": "moot"}}},
		Why: workflow.Why{Trigger: workflow.TriggerSweep, Matched: []workflow.Fact{{Name: "task.items_open", Value: 2}}}}}}
	var counted *readItems
	r := newTasksRig(t, func(d *Deps) {
		d.Intents = func() *intentload.Loaded { return pt }
		d.Plan = plan
		counted = &readItems{}
		d.Items = counted
	})
	counted.l = r.ledger

	putDerived(t, r, id, "answer Ada about the retries")
	r.put(t, task.Task{ID: "t-0000000000000009", State: "next", Category: task.CategoryReady, Title: "call Bea"})
	r.withGates(t, "t-0000000000000009")
	g, err := r.s.deps.Gates.Open(context.Background(), gate.Gate{Unit: u, Kind: gate.KindPlan, Subject: plan.items[0].ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r, pt, g
}

func wantPlanOf(t *testing.T, where string, g api.GateView, pt *intentload.Loaded, subject string) {
	t.Helper()
	i := slices.IndexFunc(pt.Workflows, func(w workflow.Workflow) bool { return w.Name == doneReacted })
	if i < 0 {
		t.Fatalf("no %s workflow loaded", doneReacted)
	}
	summary := pt.Workflows[i].Summary
	switch p := g.Plan; {
	case string(g.Subject) != `"`+subject+`"`:
		t.Errorf("%s: subject %s, want the plan item's id", where, g.Subject)
	case p == nil:
		t.Fatalf("%s: the plan gate serves no plan", where)
	case p.Summary != summary || summary == "" || p.Workflow != doneReacted:
		t.Errorf("%s: summary %q of %q, want the pt-BR summary %q", where, p.Summary, p.Workflow, summary)
	case p.Unit != task.UnitOf("t-0000000000000001") || p.Label == "":
		t.Errorf("%s: unit %v labelled %q", where, p.Unit, p.Label)
	case len(p.Why.Matched) != 1 || p.Why.Matched[0] != (api.Fact{Name: "task.items_open", Value: "2"}):
		t.Errorf("%s: matched %+v", where, p.Why.Matched)
	case !slices.Equal(p.Steps, []string{"items.resolve"}):
		t.Errorf("%s: steps %q", where, p.Steps)
	case p.Actions != nil:
		t.Errorf("%s: actions %+v, want none beside the gate's intents", where, p.Actions)
	}
	wantTargetAndSteps(t, where, *g.Plan, "t-0000000000000001", "answer Ada about the retries")
}

// wantTargetAndSteps says p acts on task id titled title, derived from its thread, and resolves its two open
// items as moot.
func wantTargetAndSteps(t *testing.T, where string, p api.PlanItem, id task.ID, title string) {
	t.Helper()
	switch tg := p.Target; {
	case tg == nil:
		t.Errorf("%s: no target", where)
	case tg.Task != string(id) || tg.Title != title:
		t.Errorf("%s: target %+v, want task %s titled %q", where, tg, id, title)
	case len(tg.Units) != 1 || tg.Units[0].Label == "" || tg.Units[0].Label == "task:"+string(id):
		t.Errorf("%s: target's conversations %+v, want its thread", where, tg.Units)
	}
	want := []api.PlanStep{{Use: "items.resolve", Reach: "local", Args: map[string]string{"resolution": "moot"},
		Items: []string{"Ana asks for the retry numbers", "Bea asks when it ships"}}}
	if mustJSON(t, p.Does) != mustJSON(t, want) {
		t.Errorf("%s: does %s, want %s", where, mustJSON(t, p.Does), mustJSON(t, want))
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestThePlanSaysWhatEachItemDoesToItsTaskFromOneReadOfTheItems(t *testing.T) {
	t.Parallel()
	r, _, _ := planGateRig(t)
	plan := r.s.deps.Plan.(*itemsPlan)
	for i := range 3 {
		id := task.ID(fmt.Sprintf("t-%016x", i+0x200))
		putDerived(t, r, id, fmt.Sprintf("reply to Dan %d", i))
		it := plan.items[0]
		it.ID, it.Unit = doneReacted+"/task/"+string(id)+"/bb", task.UnitOf(id)
		plan.items = append(plan.items, it)
	}
	reads := r.s.deps.Items.(*readItems)
	reads.byUnit.Store(0)
	reads.load.Store(0)
	var res api.PlanResponse
	if code := get(t, r.s, api.Plan, &res); code != 200 || len(res.Items) != 4 {
		t.Fatalf("plan: %d %+v", code, res)
	}
	for _, it := range res.Items {
		id, _ := task.IDOf(it.Unit)
		title := "answer Ada about the retries"
		if id != "t-0000000000000001" {
			title = fmt.Sprintf("reply to Dan %d", int(id[len(id)-1]-'0'))
		}
		wantTargetAndSteps(t, it.ID, it, id, title)
	}
	if by, load := reads.byUnit.Load(), reads.load.Load(); by != 0 || load != 1 {
		t.Errorf("the plan read the items %d times by unit and %d in batches, want one batch", by, load)
	}
}

func TestAPlanGateServesWhatItsPlanWillDo(t *testing.T) {
	t.Parallel()
	r, pt, g := planGateRig(t)
	var v api.TaskView
	if code := get(t, r.s, "/api/tasks/t-0000000000000001", &v); code != 200 {
		t.Fatalf("task: %d", code)
	}
	i := slices.IndexFunc(v.Gates, func(x api.GateView) bool { return x.ID == g.ID })
	if i < 0 {
		t.Fatalf("the task's gates %+v lack the plan gate", v.Gates)
	}
	wantPlanOf(t, "task", v.Gates[i], pt, g.Subject)

	var q api.Queue
	if code := get(t, r.s, queuePath("approve-plan"), &q); code != 200 || len(q.Items) != 1 || q.Items[0].Gate == nil {
		t.Fatalf("queue: %d %+v", code, q)
	}
	wantPlanOf(t, "queue", *q.Items[0].Gate, pt, g.Subject)

	p := r.page(t, "view=decide-approve-plan")
	if len(p.Tasks) != 1 || len(p.Tasks[0].Gates) != 1 {
		t.Fatalf("the view's tasks %+v", p.Tasks)
	}
	wantPlanOf(t, "view", p.Tasks[0].Gates[0], pt, g.Subject)
}

func TestThePlanServesEachItemsWorkflowSummary(t *testing.T) {
	t.Parallel()
	r, pt, _ := planGateRig(t)
	var res api.PlanResponse
	if code := get(t, r.s, api.Plan, &res); code != 200 || len(res.Items) != 1 {
		t.Fatalf("plan: %d %+v", code, res)
	}
	i := slices.IndexFunc(pt.Workflows, func(w workflow.Workflow) bool { return w.Name == doneReacted })
	if it := res.Items[0]; it.Summary == "" || it.Summary != pt.Workflows[i].Summary || it.Workflow != doneReacted || len(it.Actions) == 0 {
		t.Fatalf("item %+v, want its pt-BR summary beside its workflow and actions", it)
	}
}

func BenchmarkPlanGateView(b *testing.B) {
	r, _, g := planGateRig(b)
	plan := r.s.deps.Plan.(*itemsPlan)
	for i := range 200 {
		it := plan.items[0]
		it.ID = fmt.Sprintf("other/%d", i)
		plan.items = append(plan.items, it)
	}
	ctx := context.Background()
	b.ResetTimer()
	for b.Loop() {
		if v := r.s.gateView(ctx, g, nil); v.Plan == nil {
			b.Fatal("no plan")
		}
	}
}

// withMorePlanGates adds n tasks, each held by the open plan gate of a pending item of the rig's workflow.
func withMorePlanGates(t *testing.T, r *tasksRig, n int) {
	t.Helper()
	plan := r.s.deps.Plan.(*itemsPlan)
	for i := range n {
		id := task.ID(fmt.Sprintf("t-%016x", i+0x100))
		r.put(t, task.Task{ID: id, State: "next", Category: task.CategoryReady, Title: fmt.Sprintf("reply to Cid %d", i)})
		it := plan.items[0]
		it.ID, it.Unit = doneReacted+"/task/"+string(id)+"/aa", task.UnitOf(id)
		plan.items = append(plan.items, it)
		if _, err := r.s.deps.Gates.Open(context.Background(), gate.Gate{Unit: it.Unit, Kind: gate.KindPlan, Subject: it.ID}, nil); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEveryPageReadsThePlanOnceForAllItsPlanGates(t *testing.T) {
	t.Parallel()
	r, _, _ := planGateRig(t)
	withMorePlanGates(t, r, 3)
	plan := r.s.deps.Plan.(*itemsPlan)
	count := func(where string, gates func() []api.GateView) {
		t.Helper()
		plan.reads.Store(0)
		gs := gates()
		planned := 0
		for _, g := range gs {
			if g.Kind == "plan" && g.Plan != nil {
				planned++
			}
		}
		if planned != 4 {
			t.Errorf("%s: %d of %d gates with their plan, want 4", where, planned, len(gs))
		}
		if n := plan.reads.Load(); n != 1 {
			t.Errorf("%s read the plan %d times, want once", where, n)
		}
	}
	count("the view", func() []api.GateView {
		var out []api.GateView
		for _, v := range r.page(t, "view=decide-approve-plan").Tasks {
			out = append(out, v.Gates...)
		}
		return out
	})
	count("the queue", func() []api.GateView {
		var q api.Queue
		get(t, r.s, queuePath("approve-plan"), &q)
		var out []api.GateView
		for _, it := range q.Items {
			if it.Gate != nil {
				out = append(out, *it.Gate)
			}
		}
		return out
	})
	count("the gates", func() []api.GateView {
		var gs []api.GateView
		get(t, r.s, api.GatesPath+"?kind=plan", &gs)
		return gs
	})
}

func TestAPlanGateWhoseItemNoLongerWaitsServesNoPlanAnywhere(t *testing.T) {
	t.Parallel()
	r, _, g := planGateRig(t)
	r.s.deps.Plan.(*itemsPlan).items[0].Decision = workflow.DecisionRun
	var v api.TaskView
	get(t, r.s, "/api/tasks/t-0000000000000001", &v)
	var lists []api.GateView
	for _, e := range r.page(t, "view=decide-approve-plan").Tasks {
		lists = append(lists, e.Gates...)
	}
	for where, gs := range map[string][]api.GateView{"detail": v.Gates, "list": lists} {
		i := slices.IndexFunc(gs, func(x api.GateView) bool { return x.ID == g.ID })
		if i < 0 {
			t.Fatalf("%s: no plan gate in %+v", where, gs)
		}
		if gs[i].Plan != nil {
			t.Errorf("%s: plan %+v of an item that ran, want none", where, gs[i].Plan)
		}
	}
}

func TestThePlanServesEachReasonAsItsTemplateValuesAndFacts(t *testing.T) {
	t.Parallel()
	r, _, _ := planGateRig(t)
	plan := r.s.deps.Plan.(*itemsPlan)
	held := plan.items[0]
	held.Reason = workflow.Reason(workflow.ReasonHeldRefused, map[string]string{"facts": "task.items_open 2", "refusal": "the task holds no open item"})
	failed := plan.items[0]
	failed.ID, failed.Reason = "failed/x", workflow.Reason(workflow.ReasonFailed, map[string]string{"n": "3", "error": "exit 1"})
	refused := plan.items[0]
	refused.ID, refused.Reason = "refused/x", "a guard words its own refusal"
	plan.items = []workflow.Item{held, failed, refused}
	var res api.PlanResponse
	if code := get(t, r.s, api.Plan, &res); code != 200 || len(res.Items) != 3 {
		t.Fatalf("plan: %d %+v", code, res)
	}
	by := map[string]api.PlanItem{}
	for _, it := range res.Items {
		by[it.ID] = it
	}
	h := by[held.ID]
	if h.ReasonKey != workflow.ReasonHeldRefused || h.ReasonParams["refusal"] != "the task holds no open item" ||
		len(h.ReasonFacts) != 1 || h.ReasonFacts[0] != (api.Fact{Name: "task.items_open", Value: "2"}) {
		t.Errorf("held: key %q params %v facts %+v", h.ReasonKey, h.ReasonParams, h.ReasonFacts)
	}
	if f := by["failed/x"]; f.ReasonKey != workflow.ReasonFailed || f.ReasonParams["n"] != "3" || f.ReasonParams["error"] != "exit 1" {
		t.Errorf("failed: key %q params %v", f.ReasonKey, f.ReasonParams)
	}
	if x := by["refused/x"]; x.ReasonKey != "" || x.Reason != "a guard words its own refusal" {
		t.Errorf("a refusal: key %q reason %q, want the text alone", x.ReasonKey, x.Reason)
	}
}

func TestAPassesItemsServeThePlansOpenItems(t *testing.T) {
	t.Parallel()
	r, _, _ := planGateRig(t)
	ctx := context.Background()
	e, err := r.s.deps.Tasks.Task(ctx, "t-0000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	th := threadOf(e.ID)
	pass := &coretasks.Pass{Tasks: []task.Task{e}}
	ctx = coretasks.WithPass(ctx, pass)
	if _, err := coretasks.Once(ctx, coretasks.ItemsKey, func() (map[unit.Unit][]items.Item, error) {
		return map[unit.Unit][]items.Item{th: {{ID: "i1", Unit: th, Summary: "Ana asks, as the pass read it", State: items.Open}}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	reads := r.s.deps.Items.(*readItems)
	reads.load.Store(0)
	got := r.s.openItemsOf(ctx, map[task.ID]task.Task{e.ID: e})
	if want := []string{"Ana asks, as the pass read it"}; !slices.Equal(got[e.ID], want) || reads.load.Load() != 0 || reads.byUnit.Load() != 0 {
		t.Errorf("open items %q from %d batch and %d unit reads, want %q from the pass alone", got[e.ID], reads.load.Load(), reads.byUnit.Load(), want)
	}
}

func TestAnItemThatRanListsNoOpenItemsAsOfNow(t *testing.T) {
	t.Parallel()
	r, _, _ := planGateRig(t)
	plan := r.s.deps.Plan.(*itemsPlan)
	plan.items[0].Decision = workflow.DecisionRun
	var res api.PlanResponse
	if code := get(t, r.s, api.Plan+"?closed=1", &res); code != 200 || len(res.Items) != 1 {
		t.Fatalf("plan: %d %+v", code, res)
	}
	if it := res.Items[0]; it.Target == nil || len(it.Does) != 1 || len(it.Does[0].Items) != 0 {
		t.Errorf("an item that ran: target %+v does %+v, want its task and no open items of now", it.Target, it.Does)
	}
}

func TestAPlanGateWhosePlanLeftServesNoPlan(t *testing.T) {
	t.Parallel()
	r, _, g := planGateRig(t)
	r.s.deps.Plan.(*itemsPlan).items = nil
	var gs []api.GateView
	if code := get(t, r.s, api.GatesPath+"?kind=plan", &gs); code != 200 || len(gs) != 1 {
		t.Fatalf("gates: %d %+v", code, gs)
	}
	if gs[0].ID != g.ID || gs[0].Plan != nil || gs[0].Text == "" {
		t.Fatalf("gate %+v, want its question and no plan", gs[0])
	}
}
