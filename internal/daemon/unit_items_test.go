package daemon

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestUnitDetailTiesEachItemToWhomItWaitsOnAndItsTask(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	ctx := context.Background()
	create := func(id, summary string, waits []items.Person) items.Op {
		f := &items.Fields{Kind: ptrOf(items.Request), Summary: ptrOf(summary)}
		if waits != nil {
			f.WaitingOn = &waits
		}
		return items.Op{Op: items.OpCreate, Unit: r7u, Item: id, Anchor: unit.Anchor{Ref: "user:" + id}, Fields: f}
	}
	ops := []items.Op{
		create("i1", "Review the retry change", []items.Person{{Key: "github:me", Name: "Me"}, {Key: "github:ana", Name: "Ana Lima"}}),
		create("i2", "Pick a release date", nil),
		create("i3", "Rename the flag", nil),
		{Op: items.OpResolve, Unit: r7u, Item: "i3", Anchor: unit.Anchor{Ref: "user:i3r"}, Resolution: items.Done},
		create("i4", "Drop the shim", nil),
		{Op: items.OpResolve, Unit: r7u, Item: "i4", Anchor: unit.Anchor{Ref: "user:i4r"}, Resolution: items.Done},
	}
	if _, err := r.ledger.Apply(ctx, items.User, ops); err != nil {
		t.Fatal(err)
	}
	r.put(t,
		task.Task{ID: "t-0", Title: "Old review", State: "done", Category: task.CategoryDone, Resolved: []task.ItemRef{{Unit: r7u, Item: "i1"}}},
		task.Task{ID: "t-1", Title: "Review the retries", State: "next", Category: task.CategoryReady, Items: []task.ItemRef{{Unit: r7u, Item: "i1"}}},
		task.Task{ID: "t-2", Title: "Tidy the client", State: "done", Category: task.CategoryDone, Resolved: []task.ItemRef{{Unit: r7u, Item: "i4"}}})

	var d api.Detail
	if code := get(t, r.s, r7Detail, &d); code != 200 {
		t.Fatalf("detail: %d", code)
	}
	if len(d.Items) != 4 {
		t.Fatalf("items %+v", d.Items)
	}
	i1 := d.ItemTies["i1"]
	if i1.Task != "t-1" || i1.TaskTitle != "Review the retries" {
		t.Errorf("i1 is held by %q %q, want the open t-1 over the done t-0", i1.Task, i1.TaskTitle)
	}
	if i4 := d.ItemTies["i4"]; i4.Task != "t-2" || i4.TaskTitle != "Tidy the client" {
		t.Errorf("i4 is held by %q %q, want t-2, whose resolved items hold it", i4.Task, i4.TaskTitle)
	}
	want := []api.ItemPerson{{Key: "github:me", Name: "Me", Me: true}, {Key: "github:ana", Name: "Ana Lima"}}
	if len(i1.WaitsOn) != 2 || i1.WaitsOn[0] != want[0] || i1.WaitsOn[1] != want[1] {
		t.Errorf("i1 waits on %+v, want %+v", i1.WaitsOn, want)
	}
	for _, id := range []string{"i2", "i3"} {
		if tie := d.ItemTies[id]; tie.Task != "" || tie.TaskTitle != "" || len(tie.WaitsOn) != 0 {
			t.Errorf("%s: %+v, want no owner and no task", id, tie)
		}
	}
}
