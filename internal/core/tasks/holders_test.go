package tasks_test

import (
	"context"
	"slices"
	"testing"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestHoldersOfListOpenTasksFirstAndLeaveOutThoseOfADisabledPlugin(t *testing.T) {
	t.Parallel()
	r := newRig()
	slackU := unit.Unit{Provider: "slack", Key: "T1/C1/1700000000.000100"}
	off := []unit.ProviderID{"slack"}
	r.svc = coretasks.New(coretasks.Deps{Store: r.store, Items: r.items, Facts: r.facts, Me: me, Clock: r.clock, States: taskstest.BundledStates,
		Disabled: func() []unit.ProviderID { return off }})
	r.put(t, task.Task{ID: "t-done", State: "done", Origin: task.OriginDerived, Resolved: []task.ItemRef{{Unit: unitU, Item: "i1"}}})
	r.put(t, task.Task{ID: "t-open", State: "next", Origin: task.OriginDerived, Items: []task.ItemRef{{Unit: unitU, Item: "i1"}}})
	r.put(t, task.Task{ID: "t-off", State: "next", Origin: task.OriginDerived, Units: []unit.Unit{slackU}, Items: []task.ItemRef{{Unit: unitU, Item: "i2"}}})
	es, err := r.svc.HoldersOf(context.Background(), unitU)
	if err != nil {
		t.Fatal(err)
	}
	var ids []task.ID
	for _, e := range es {
		ids = append(ids, e.ID)
	}
	if want := []task.ID{"t-open", "t-done"}; !slices.Equal(ids, want) {
		t.Errorf("holders %v, want %v: open first, a closed holder kept, a disabled plugin's task left out", ids, want)
	}
}

func TestMeTakesNotTheServicesLock(t *testing.T) {
	t.Parallel()
	r := newRig()
	r.put(t, task.Task{ID: "t-1", State: "inbox", Title: "a", Origin: task.OriginUser})
	var got task.Me
	coretasks.SetRaceHooks(r.svc, func() { got = r.svc.Me() }, func() { t.Fatal("Me waited on the lock a verb held") })
	if _, err := r.svc.Do(context.Background(), "t-1", coretasks.Request{Actor: task.ActorPerson, Verb: task.VerbNext}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Keys, me.Keys) {
		t.Errorf("Me %v, want %v", got.Keys, me.Keys)
	}
}
