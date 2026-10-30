package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	intentload "github.com/wloophq/wloop/internal/intent"
)

// profileGates is gates.toml as the gates service reads it.
type profileGates struct{ d intentload.GatesDef }

func (p profileGates) Gate(name string) (gates.Def, bool) {
	g, ok := p.d.Gate(name)
	return defOf(g), ok
}

func (p profileGates) FirstOf(k gate.Kind) (gates.Def, bool) {
	g, ok := p.d.FirstOf(k)
	return defOf(g), ok
}

func defOf(g intentload.GateDef) gates.Def {
	d := gates.Def{Name: g.Name, Kind: g.Kind, Label: g.Label}
	for _, in := range g.Intents {
		d.Intents = append(d.Intents, gates.Intent{Name: in.Name, Status: in.Status, Answer: in.Answer})
	}
	return d
}

// withGates serves the gates of r's store, and opens a question on task id.
func (r *tasksRig) withGates(t *testing.T, id task.ID) gate.Gate {
	t.Helper()
	st := r.db.Gates(func() time.Time { return tasksNow })
	svc := gates.New(gates.Deps{Store: st, Reader: st, Profile: profileGates{r.s.loaded().Gates}, Clock: fixedClock(tasksNow)})
	r.s.deps.Gates, r.s.deps.GateReader = svc, st
	g, err := svc.Open(context.Background(), gate.Gate{Unit: task.UnitOf(id), Kind: gate.KindQuestion, Subject: "7"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestGatePathRefusesAnAgentSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newTasksRig(t, nil)
	r.put(t, task.Task{ID: "t-0000000000000001", State: "next", Category: task.CategoryReady, Title: "ship it"})
	g := r.withGates(t, "t-0000000000000001")
	path := "/api/gates/1/dismiss"
	if rec := postFrom(t, r.s, "term_1", path, `{"source":"claude"}`); rec.Code != 403 {
		t.Fatalf("an agent session's resolution: %d %s", rec.Code, rec.Body)
	}
	if got, _ := r.s.deps.GateReader.Get(ctx, g.ID); got.Status != gate.Open {
		t.Fatalf("the refused resolution left the gate %s", got.Status)
	}
	rec := post(t, r.s, path, `{"source":"cockpit"}`)
	if rec.Code != 200 {
		t.Fatalf("a person's resolution: %d %s", rec.Code, rec.Body)
	}
	if got, _ := r.s.deps.GateReader.Get(ctx, g.ID); got.Status != gate.Dismissed || got.By != task.ActorPerson {
		t.Fatalf("gate = %+v, want dismissed by a person", got)
	}
	if rec := post(t, r.s, "/api/gates/1/dismiss", `{}`); rec.Code != 409 {
		t.Fatalf("resolving a closed gate: %d %s", rec.Code, rec.Body)
	}
}

func TestATaskServesItsOpenGates(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.put(t, task.Task{ID: "t-0000000000000001", State: "next", Category: task.CategoryReady, Title: "ship it"})
	g := r.withGates(t, "t-0000000000000001")
	var v api.TaskView
	if code := get(t, r.s, api.TaskPath("t-0000000000000001"), &v); code != 200 {
		t.Fatalf("task detail: %d", code)
	}
	if len(v.Gates) != 1 {
		t.Fatalf("gates = %+v, want the open question", v.Gates)
	}
	gv := v.Gates[0]
	if gv.ID != g.ID || gv.Kind != "question" || gv.Text != "The agent asks" || gv.Task != "t-0000000000000001" ||
		len(gv.Intents) != 3 || gv.Intents[0].ID != "answer" || !gv.Intents[0].Answer || gv.Intents[2].Label != "Dismiss" {
		t.Fatalf("gate view = %+v", gv)
	}
	var open []api.GateView
	if code := get(t, r.s, api.GatesPath, &open); code != 200 || len(open) != 1 {
		t.Fatalf("open gates: %d %+v", code, open)
	}
	if rec := post(t, r.s, "/api/gates/1/answer", `{"args":{"answer":"ship it friday"}}`); rec.Code != 200 {
		t.Fatalf("answer: %d %s", rec.Code, rec.Body)
	}
	var none, answered []api.GateView
	if get(t, r.s, api.GatesPath, &none); len(none) != 0 {
		t.Fatalf("open gates after the answer: %+v", none)
	}
	if get(t, r.s, api.GatesPath+"?status=answered", &answered); len(answered) != 1 || answered[0].Status != "answered" {
		t.Fatalf("answered gates: %+v", answered)
	}
}

func TestGatesListFiltersByKind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newTasksRig(t, nil)
	id := task.ID("t-0000000000000002")
	r.put(t, task.Task{ID: id, State: "next", Category: task.CategoryReady, Title: "ship it"})
	question := r.withGates(t, id)
	plan, err := r.s.deps.Gates.Open(ctx, gate.Gate{Unit: task.UnitOf(id), Kind: gate.KindPlan, Subject: "round"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var all, plans []api.GateView
	if code := get(t, r.s, api.GatesPath, &all); code != 200 || len(all) != 2 {
		t.Fatalf("every open gate: %d %+v", code, all)
	}
	if code := get(t, r.s, api.GatesPath+"?status=open&kind=plan", &plans); code != 200 || len(plans) != 1 || plans[0].ID != plan.ID || plans[0].ID == question.ID {
		t.Fatalf("the plan gates: %d %+v", code, plans)
	}
}

func TestClosingATaskSettlesTheGatesGatesTomlClosesWith(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, verb := range []string{"done", "dismiss"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			r := newTasksRig(t, nil)
			r.put(t, task.Task{ID: "t-0000000000000001", State: "next", Category: task.CategoryReady, Title: "ship it"})
			g := r.withGates(t, "t-0000000000000001")
			if rec := post(t, r.s, api.TaskPath("t-0000000000000001", verb), `{}`); rec.Code != 200 {
				t.Fatalf("%s: %d %s", verb, rec.Code, rec.Body)
			}
			if got, _ := r.s.deps.GateReader.Get(ctx, g.ID); got.Status != gate.Dismissed || got.Intent != "dismiss" || got.By != task.ActorPerson {
				t.Fatalf("the question after %s = %+v, want dismissed by the person", verb, got)
			}
			if e, err := r.g.Task(ctx, "t-0000000000000001"); err != nil || e.Open() {
				t.Fatalf("task after %s = %+v, %v; want it closed", verb, e, err)
			}
		})
	}
}
