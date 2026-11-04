package task_test

import (
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
)

func TestActivityOnATaskParkedOnSomeoneMarksItChangedAndKeepsItOutOfTheInbox(t *testing.T) {
	f := facts(func(f *task.UnitFacts) { f.LastHuman = now })
	its := []items.Item{
		{ID: "i-them", Unit: pr, State: "open", WaitingOn: []items.Person{{Key: "github:bob"}}},
		{ID: "i-me", Unit: pr, State: "open", WaitingOn: []items.Person{{Key: "github:me"}}},
	}
	owned := task.Task{ID: "t-owned", State: "waiting", Category: task.CategoryReady, Created: now.Add(-time.Hour)}
	owned.SetProp(task.PropOwner, task.Value{Actors: []items.Person{{Key: "slack:U1"}}}, task.WriterPerson, now.Add(-time.Hour))
	mineToo := task.Task{ID: "t-owned-by-me", State: "waiting", Category: task.CategoryReady, Created: now.Add(-time.Hour)}
	mineToo.SetProp(task.PropOwner, task.Value{Actors: []items.Person{{Key: "github:me"}}}, task.WriterPerson, now.Add(-time.Hour))
	checked := task.Task{ID: "t-check-on", State: "waiting", Category: task.CategoryReady, Created: now.Add(-time.Hour)}
	checked.SetProp(task.PropCheckOn, task.Value{Time: now.Add(48 * time.Hour)}, task.WriterPerson, now.Add(-time.Hour))
	have := []task.Task{
		owned,
		checked,
		{ID: "t-held", State: "next", Category: task.CategoryReady, Created: now.Add(-time.Hour), Items: []task.ItemRef{{Unit: pr, Item: "i-them"}}},
		{ID: "t-scheduled", State: "scheduled", Category: task.CategoryDeferred, Asleep: true, Created: now.Add(-time.Hour)},
		{ID: "t-mine", State: "next", Category: task.CategoryReady, Created: now.Add(-time.Hour), Items: []task.ItemRef{{Unit: pr, Item: "i-me"}}},
		{ID: "t-next", State: "next", Category: task.CategoryReady, Created: now.Add(-time.Hour)},
		{ID: "t-inbox", State: "inbox", Category: task.CategoryInbox, Created: now.Add(-time.Hour), Items: []task.ItemRef{{Unit: pr, Item: "i-them"}}},
		{ID: "t-done", State: "done", Category: task.CategoryDone, Created: now.Add(-time.Hour), Items: []task.ItemRef{{Unit: pr, Item: "i-them"}}},
		{ID: "t-seen", State: "next", Category: task.CategoryReady, Created: now.Add(-time.Hour), SeenAt: now, Items: []task.ItemRef{{Unit: pr, Item: "i-them"}}},
		mineToo,
	}
	cs := task.Activity(f, its, me, have, now.Add(time.Minute))
	var got []task.ID
	for _, c := range cs {
		got = append(got, c.Task.ID)
	}
	if want := []task.ID{"t-owned", "t-check-on", "t-held", "t-scheduled"}; !equalIDs(got, want) {
		t.Fatalf("activity changed %v, want %v", got, want)
	}
	for i, c := range cs {
		if !c.Task.Changed || c.Task.State != have[i].State || c.Task.Category != have[i].Category || c.Task.Asleep != have[i].Asleep {
			t.Errorf("%s: %+v, want it changed where it was", c.Task.ID, c.Task)
		}
	}
	if again := task.Activity(f, its, me, []task.Task{cs[0].Task}, now.Add(time.Hour)); len(again) != 0 {
		t.Fatalf("activity marked a changed task again: %+v", again)
	}
}

func equalIDs(a, b []task.ID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
