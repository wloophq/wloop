package tasks_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func waitingOnAna(t *testing.T, r *rig) task.ID {
	t.Helper()
	it := items.Item{ID: "a", Unit: unitU, Summary: "review", State: "open", Requester: meP, WaitingOn: []items.Person{ana}, CreatedAt: t0}
	r.items.byUnit[unitU] = []items.Item{it}
	if err := r.svc.Sync(context.Background(), []unit.Unit{unitU}); err != nil {
		t.Fatal(err)
	}
	id := task.DerivedID(unitU, "a")
	if e := r.task(t, id); e.Category != task.CategoryWaiting {
		t.Fatalf("%+v", e)
	}
	return id
}

func TestNudgedStampsForwardOnly(t *testing.T) {
	ctx := context.Background()
	r := newRig()
	id := waitingOnAna(t, r)
	at := t0.Add(time.Hour)
	if err := r.svc.Nudged(ctx, "t-gone", unitU, []string{"a"}, at); err != nil {
		t.Fatal(err)
	}
	if e := r.task(t, id); !e.Prop(task.PropLastNudge).Time.Equal(at) || e.Written[task.PropLastNudge].By != task.WriterWorkflow {
		t.Fatalf("%+v", e.Props)
	}
	if err := r.svc.Nudged(ctx, id, unitU, nil, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if e := r.task(t, id); !e.Prop(task.PropLastNudge).Time.Equal(at) {
		t.Fatalf("moved back: %+v", e.Props)
	}
	later := t0.Add(2 * time.Hour)
	if err := r.svc.Nudged(ctx, "", unitU, []string{"a"}, later); err != nil {
		t.Fatal(err)
	}
	if e := r.task(t, id); !e.Prop(task.PropLastNudge).Time.Equal(later) {
		t.Fatalf("by item: %+v", e.Props)
	}
}

func TestNudgeStampKeepsAConcurrentSyncMove(t *testing.T) {
	ctx := context.Background()
	for round := range 30 {
		r := newRig()
		id := waitingOnAna(t, r)
		r.facts.facts[unitU] = task.UnitFacts{Unit: unitU, LastHuman: t0.Add(time.Minute)}
		r.clock.t = t0.Add(2 * time.Minute)
		at := t0.Add(90 * time.Second)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := r.svc.Sync(ctx, []unit.Unit{unitU}); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := r.svc.Nudged(ctx, id, unitU, nil, at); err != nil {
				t.Error(err)
			}
		}()
		wg.Wait()
		e := r.task(t, id)
		if e.Category != task.CategoryWaiting || !e.Changed || !e.Prop(task.PropLastNudge).Time.Equal(at) {
			t.Fatalf("round %d: %s changed=%v lastNudge=%v", round, e.State, e.Changed, e.Prop(task.PropLastNudge).Time)
		}
	}
}
