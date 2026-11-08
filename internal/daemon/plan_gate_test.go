package daemon

import (
	"context"
	"fmt"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/domain/gate"
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

// planGateRig is a pt-BR daemon with task t-0000000000000001 held by the open plan gate of a slack-done-reacted
// item on it.
func planGateRig(t testing.TB) (*tasksRig, *intentload.Loaded, gate.Gate) {
	t.Helper()
	pt := loadedIn(t, "pt-BR")
	id := task.ID("t-0000000000000001")
	u := task.UnitOf(id)
	plan := &itemsPlan{items: []workflow.Item{{ID: doneReacted + "/task/t-0000000000000001/28f2c473", Workflow: doneReacted, Unit: u,
		Decision: workflow.DecisionPending, Steps: []workflow.Step{{Use: "items.resolve"}},
		Why: workflow.Why{Trigger: workflow.TriggerSweep, Matched: []workflow.Fact{{Name: "task.items_open", Value: 2}}}}}}
	r := newTasksRig(t, func(d *Deps) {
		d.Intents = func() *intentload.Loaded { return pt }
		d.Plan = plan
	})
	r.put(t, task.Task{ID: id, State: "next", Category: task.CategoryReady, Title: "answer Ada about the retries"},
		task.Task{ID: "t-0000000000000009", State: "next", Category: task.CategoryReady, Title: "call Bea"})
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
