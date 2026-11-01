package cli

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestATasksDraftPostsOnTheUnitItsItemsAreOn(t *testing.T) {
	t.Parallel()
	r := newDraftRig(t, draftWF("nudges", "purpose", "nudge", "items", "owed", "per", "person"), "")
	pr := unit.Unit{Provider: unit.GitHub, Key: "acme/api#9"}
	owed := openItem("i1", "Review the copy", r.now, ana)
	r.items.set(draftThread, owed)
	r.tasks.m["t1"] = task.Task{ID: "t1", State: "waiting", Category: task.CategoryWaiting, Units: []unit.Unit{pr, draftThread},
		Items: []task.ItemRef{{Unit: draftThread, Item: owed.ID}}}
	sub, err := r.deps.subject(context.Background(), draftTask)
	if err != nil {
		t.Fatal(err)
	}
	if sub.unit != draftThread {
		t.Fatalf("posts on %s, want the thread its item is on, not the first unit", sub.unit)
	}
	r.tasks.m["t1"] = task.Task{ID: "t1", State: "next", Category: task.CategoryReady, Units: []unit.Unit{pr, draftThread}}
	if sub, err = r.deps.subject(context.Background(), draftTask); err != nil || sub.unit != pr {
		t.Fatalf("a task holding no item posts on %s (%v), want its first unit", sub.unit, err)
	}
}

func TestAPersonAsksYouWhenTheyWaitOnYouOrAssignedYouAlone(t *testing.T) {
	t.Parallel()
	thread := unit.Unit{Provider: "slack", Key: "T1/C1/1.1"}
	me := task.Me{Keys: []string{"slack:UME"}}
	meP, bruno, carla := items.Person{Key: "slack:UME"}, items.Person{Key: "slack:UBRUNO"}, items.Person{Key: "slack:UCARLA"}
	ask := func(assignees, waiting []items.Person) items.Item {
		return items.Item{ID: "a1", Unit: thread, State: items.Open, Requester: bruno, Assignees: assignees, WaitingOn: waiting}
	}
	cases := []struct {
		name string
		it   items.Item
		want bool
	}{
		{"waits on me", ask(nil, []items.Person{meP}), true},
		{"assigned to me, waiting on nobody", ask([]items.Person{meP}, nil), true},
		{"assigned to me, waiting on Carla", ask([]items.Person{meP}, []items.Person{carla}), false},
		{"waits on Carla", ask(nil, []items.Person{carla}), false},
	}
	for _, c := range cases {
		sub := draftSubject{unit: thread, items: []items.Item{c.it}}
		if got := sub.asks(bruno.Key, me); got != c.want {
			t.Errorf("%s: asks = %v, want %v", c.name, got, c.want)
		}
	}
}
