package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestATaskPickingUpAUnitBackFillsItsGates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "s.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	g := db.Gates(func() time.Time { return noteAt })
	thread := unit.Unit{Provider: "slack", Key: "T1/C1/1.1"}
	free, err := g.Open(ctx, gate.Gate{Unit: thread, Kind: gate.KindQuestion, Name: "question", Subject: "1"})
	if err != nil {
		t.Fatal(err)
	}
	closed := task.Task{ID: "t-00000000000000d1", State: "done", Category: task.CategoryDone, Units: []unit.Unit{thread}, Origin: task.OriginDerived, Created: noteAt, Updated: noteAt}
	if err := db.Tasks().Apply(ctx, []task.Change{{Op: task.OpCreate, Task: closed}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := g.Get(ctx, free.ID); got.Task != "" {
		t.Fatalf("a done task took the gate: %+v", got)
	}
	open := task.Task{ID: "t-00000000000000d2", State: "inbox", Category: task.CategoryInbox, Items: []task.ItemRef{{Unit: thread, Item: "i1"}}, Origin: task.OriginDerived, Created: noteAt, Updated: noteAt}
	if err := db.Tasks().Apply(ctx, []task.Change{{Op: task.OpCreate, Task: open}}); err != nil {
		t.Fatal(err)
	}
	if got, err := g.Get(ctx, free.ID); err != nil || got.Task != open.ID {
		t.Fatalf("the gate after a task took its unit: %+v %v", got, err)
	}
}
