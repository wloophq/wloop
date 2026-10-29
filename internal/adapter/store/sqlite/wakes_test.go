package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/wakes"
	"github.com/wloophq/wloop/internal/core/wakes/wakestest"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/wake"
)

func TestWakeStoreContract(t *testing.T) {
	t.Parallel()
	wakestest.StoreContract(t, func(t *testing.T) wakes.Store {
		db, _ := openTemp(t)
		for _, id := range []task.ID{"t-01", "t-02", "t-03"} {
			applyTask(t, db, id)
		}
		return db.Wakes()
	})
}

func TestATaskWithAWakeIsNotSomedayAndClosingItDropsTheWake(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := openTemp(t)
	at := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	pr := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	asleep := task.Task{ID: "t-asleep", State: "scheduled", Category: task.CategoryDeferred, Units: []unit.Unit{pr}, Origin: task.OriginUser, Created: at, Updated: at}
	someday := task.Task{ID: "t-someday", State: "someday", Category: task.CategoryDeferred, Origin: task.OriginUser, Created: at.Add(time.Second), Updated: at}
	if err := db.Tasks().Apply(ctx, []task.Change{{Op: task.OpCreate, Task: asleep}, {Op: task.OpCreate, Task: someday}}); err != nil {
		t.Fatal(err)
	}
	if err := db.Wakes().Put(ctx, wake.Wake{Task: asleep.ID, Until: at.Add(time.Hour), Return: "next"}); err != nil {
		t.Fatal(err)
	}
	got, err := db.Tasks().Tasks(ctx, task.Query{View: task.ViewSomeday})
	if err != nil || len(got) != 1 || got[0].ID != someday.ID {
		t.Fatalf("someday %+v %v", got, err)
	}
	if c, err := db.Tasks().Counts(ctx, ""); err != nil || c.Views[task.ViewSomeday] != 1 {
		t.Fatalf("counts %+v %v", c, err)
	}
	if e, err := db.Tasks().Task(ctx, asleep.ID); err != nil || !e.Asleep || !e.Snoozed() {
		t.Fatalf("the asleep task %+v %v", e, err)
	}
	if ws, err := db.Wakes().UnitWakes(ctx, []unit.Unit{pr}); err != nil || ws[pr].Task != asleep.ID {
		t.Fatalf("the unit's wake %+v %v", ws, err)
	}
	done := asleep
	done.State, done.Category = "done", task.CategoryDone
	if err := db.Tasks().Apply(ctx, []task.Change{{Op: task.OpResolve, Task: done}}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := db.Wakes().Of(ctx, asleep.ID); ok || err != nil {
		t.Fatalf("a done task kept its wake: %v %v", ok, err)
	}
}
