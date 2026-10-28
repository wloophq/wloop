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
	r.tasks.m["e1"] = task.Task{ID: "e1", Kind: task.KindCapture, State: "waiting", Category: task.CategoryWaiting, Units: []unit.Unit{pr, draftThread},
		Items: []task.ItemRef{{Unit: draftThread, Item: owed.ID}}, Waiting: &task.WaitingOn{On: []items.Person{ana}, Since: r.now}}
	sub, err := r.deps.subject(context.Background(), draftTask)
	if err != nil {
		t.Fatal(err)
	}
	if sub.unit != draftThread {
		t.Fatalf("posts on %s, want the thread its item is on, not the first unit", sub.unit)
	}
	r.tasks.m["e1"] = task.Task{ID: "e1", Kind: task.KindCapture, State: "next", Category: task.CategoryReady, Units: []unit.Unit{pr, draftThread}}
	if sub, err = r.deps.subject(context.Background(), draftTask); err != nil || sub.unit != pr {
		t.Fatalf("an entry holding no item posts on %s (%v), want its first unit", sub.unit, err)
	}
}
