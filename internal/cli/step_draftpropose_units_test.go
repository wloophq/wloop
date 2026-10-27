package cli

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestAnEntrysDraftPostsOnTheUnitItsItemsAreOn(t *testing.T) {
	t.Parallel()
	r := newDraftRig(t, draftWF("nudges", "purpose", "nudge", "items", "owed", "per", "person"), "")
	pr := unit.Unit{Provider: unit.GitHub, Key: "acme/api#9"}
	owed := openItem("i1", "Review the copy", r.now, ana)
	r.items.set(draftThread, owed)
	r.entries.m["e1"] = gtd.Entry{ID: "e1", Kind: gtd.KindCapture, State: gtd.Waiting, Units: []unit.Unit{pr, draftThread},
		Items: []gtd.ItemRef{{Unit: draftThread, Item: owed.ID}}, Waiting: &gtd.WaitingOn{On: []items.Person{ana}, Since: r.now}}
	sub, err := r.deps.subject(context.Background(), draftEntry)
	if err != nil {
		t.Fatal(err)
	}
	if sub.unit != draftThread {
		t.Fatalf("posts on %s, want the thread its item is on, not the first unit", sub.unit)
	}
	r.entries.m["e1"] = gtd.Entry{ID: "e1", Kind: gtd.KindCapture, State: gtd.Next, Units: []unit.Unit{pr, draftThread}}
	if sub, err = r.deps.subject(context.Background(), draftEntry); err != nil || sub.unit != pr {
		t.Fatalf("an entry holding no item posts on %s (%v), want its first unit", sub.unit, err)
	}
}
