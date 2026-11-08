package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel"
)

// forgeRows serves a change request's row with its title, reference and link.
type forgeRows struct{ threadRows }

func (forgeRows) Derive(_ readmodel.Base, in any, _ time.Time) (api.Row, error) {
	return api.Row{Title: "Batch export", Label: "acme/api#9", Link: "https://forge.example/acme/api/changes/" + in.(string), Views: []string{}}, nil
}

func TestAStepGateServesTheUnitsItIsAboutAndWhatEachAnswerDoes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	change := unit.Unit{Provider: "thread", Key: "9"}
	r := newTasksRig(t, func(d *Deps) {
		d.RowProviders = append(d.RowProviders, func(readmodel.RowDeps) readmodel.Provider { return forgeRows{threadRows{units: []unit.Unit{change}}} })
	})
	id := task.ID("t-0000000000000001")
	r.put(t, task.Task{ID: id, State: "next", Category: task.CategoryReady, Title: "ship the export", Units: []unit.Unit{change}},
		task.Task{ID: "t-0000000000000009", State: "next", Category: task.CategoryReady, Title: "call Bea"})
	r.withGates(t, "t-0000000000000009")
	g, err := r.s.deps.Gates.Open(ctx, gate.Gate{Name: "review-diff", Unit: task.UnitOf(id), Subject: "start-review-diff",
		Text: "Review the diff of acme/api#9, 3 files changed"}, gates.Policy(nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.readModel().Current(ctx); err != nil {
		t.Fatal(err)
	}
	want := func(where string, v *api.GateView) {
		t.Helper()
		switch {
		case v == nil || v.ID != g.ID:
			t.Fatalf("%s: gate %+v, want %d", where, v, g.ID)
		case v.Text != "Review the diff of acme/api#9, 3 files changed":
			t.Errorf("%s: text %q", where, v.Text)
		case len(v.Units) != 1 || v.Units[0] != (api.PlanUnit{Label: "acme/api#9", Title: "Batch export", Link: "https://forge.example/acme/api/changes/9"}):
			t.Errorf("%s: units %+v, want the change it is about with its title and link", where, v.Units)
		case len(v.Intents) != 2 || v.Intents[0].Summary != "Mark the diff reviewed; posts nothing." || v.Intents[1].Summary == "":
			t.Errorf("%s: intents %+v, want each with what it does", where, v.Intents)
		}
	}
	var q api.Queue
	if code := get(t, r.s, queuePath("review-step"), &q); code != 200 || len(q.Items) != 1 {
		t.Fatalf("%d %+v", code, q)
	}
	want("queue", q.Items[0].Gate)
	var v api.TaskView
	if code := get(t, r.s, "/api/tasks/"+string(id), &v); code != 200 || len(v.Gates) != 1 {
		t.Fatalf("%d %+v", code, v.Gates)
	}
	want("detail", &v.Gates[0])

	merged := unit.Unit{Provider: "thread", Key: "8"}
	r.put(t, task.Task{ID: "t-0000000000000002", State: "next", Category: task.CategoryReady, Title: "two changes", Units: []unit.Unit{merged, change}})
	r.s.deps.UnitEnded = func(_ context.Context, u unit.Unit) (bool, error) { return u == merged, nil }
	g2, err := r.s.deps.Gates.Open(ctx, gate.Gate{Name: "review-diff", Unit: task.UnitOf("t-0000000000000002"), Subject: "start-review-diff"}, gates.Policy(nil))
	if err != nil {
		t.Fatal(err)
	}
	var two api.TaskView
	get(t, r.s, "/api/tasks/t-0000000000000002", &two)
	if len(two.Gates) != 1 || two.Gates[0].ID != g2.ID || len(two.Gates[0].Units) != 2 || two.Gates[0].Units[0].Link != "https://forge.example/acme/api/changes/9" {
		t.Errorf("a step on a merged change and an open one: %+v, want the open one first", two.Gates)
	}

	var other api.TaskView
	get(t, r.s, "/api/tasks/t-0000000000000009", &other)
	if len(other.Gates) != 1 || other.Gates[0].Units != nil {
		t.Errorf("a question's gate serves units %+v", other.Gates)
	}
	for _, in := range other.Gates[0].Intents {
		if in.Summary != "" {
			t.Errorf("the bundled question gate, whose gates.toml sets no card_says, serves %s's summary %q for its card", in.ID, in.Summary)
		}
	}
}
