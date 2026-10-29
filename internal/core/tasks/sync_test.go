package tasks_test

import (
	"context"
	"testing"
	"time"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestActivityMovesOnlyItsUnitsTasks(t *testing.T) {
	ctx := context.Background()
	r := newRig()
	r.items.byUnit[unitU] = []items.Item{owed(unitU, "a")}
	if err := r.svc.Sync(ctx, []unit.Unit{unitU}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Do(ctx, task.DerivedID(unitU, "a"), coretasks.Request{Actor: task.ActorPerson, Verb: task.VerbSchedule,
		Args: map[string]string{"on_activity": "true"}}); err != nil {
		t.Fatal(err)
	}
	r.facts.facts[unitV] = task.UnitFacts{Unit: unitV, LastHuman: t0.Add(time.Hour)}
	if err := r.svc.Sync(ctx, []unit.Unit{unitU, unitV}); err != nil {
		t.Fatal(err)
	}
	if e := r.task(t, task.DerivedID(unitU, "a")); e.Category != task.CategoryDeferred || e.Changed {
		t.Fatalf("activity on V moved U's entry: %+v", e)
	}
}
