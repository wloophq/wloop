package tasks_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/link"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func eventsOf(evs []port.Event, kind string) []port.Event {
	return slices.DeleteFunc(slices.Clone(evs), func(ev port.Event) bool { return ev.Kind != kind })
}

func TestSyncRecordsTaskCreatedOnANewTask(t *testing.T) {
	ctx := context.Background()
	r := newRig()
	r.items.byUnit[unitU] = []items.Item{owed(unitU, "a")}
	if err := r.svc.Sync(ctx, []unit.Unit{unitU}); err != nil {
		t.Fatal(err)
	}
	id := task.DerivedID(unitU, "a")
	want := []port.Event{{Unit: task.UnitOf(id), Kind: task.EventCreated, At: t0}}
	if got := eventsOf(r.store.Events, task.EventCreated); !slices.Equal(got, want) {
		t.Fatalf("events %+v, want %+v", got, want)
	}
	r.clock.t = t0.Add(time.Hour)
	r.items.byUnit[unitU][0].Summary = "do a, and soon"
	if err := r.svc.Sync(ctx, []unit.Unit{unitU}); err != nil {
		t.Fatal(err)
	}
	if got := eventsOf(r.store.Events, task.EventCreated); !slices.Equal(got, want) {
		t.Fatalf("an update recorded a creation: %+v", got)
	}
}

func TestALinkOnAHeldItemRecordsTaskLinked(t *testing.T) {
	ctx := context.Background()
	r := newRig()
	r.items.byUnit[unitU] = []items.Item{owed(unitU, "a")}
	if err := r.svc.Sync(ctx, []unit.Unit{unitU}); err != nil {
		t.Fatal(err)
	}
	pr := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	r.clock.t = t0.Add(time.Minute)
	if err := r.svc.Linked(ctx, []link.Link{
		{From: unitU, To: pr, Kind: link.Review, Item: "a", At: t0},
		{From: unitU, To: pr, Kind: link.Mention, At: t0},
		{From: unitU, To: pr, Kind: link.Review, Item: "nobody-holds", At: t0},
		{From: unitV, To: pr, Kind: link.Merge, Item: "a", At: t0},
	}); err != nil {
		t.Fatal(err)
	}
	got := eventsOf(r.store.Events, task.EventLinked)
	if len(got) != 1 {
		t.Fatalf("events %+v, want one, of the held item's link", got)
	}
	if ev := got[0]; ev.Unit != task.UnitOf(task.DerivedID(unitU, "a")) || ev.Key == "" || ev.At != r.clock.t || ev.Data != pr.String() {
		t.Fatalf("event %+v", ev)
	}
}

func TestATaskAPersonOpensOnAUnitRecordsTaskCreated(t *testing.T) {
	ctx := context.Background()
	r := newRig()
	u := unitU
	on, err := r.svc.Open(ctx, coretasks.OpenRequest{Title: "Look at the fix", Unit: &u, Origin: task.OriginUser})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Capture(ctx, "Buy the tickets", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Open(ctx, coretasks.OpenRequest{Title: "Send this reply?", Unit: &u}); err != nil {
		t.Fatal(err)
	}
	want := []port.Event{{Unit: task.UnitOf(on.ID), Kind: task.EventCreated, At: t0}}
	if got := eventsOf(r.store.Events, task.EventCreated); !slices.Equal(got, want) {
		t.Fatalf("events %+v, want %+v: the task a person opened on a unit only", got, want)
	}
}
