package cli

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/timeline"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

func TestATasksTimelineLeavesTheLoopsBookkeepingOut(t *testing.T) {
	ctx := context.Background()
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	const id = task.ID("t-3333333333333333")
	u := task.UnitOf(id)
	var evs []port.Event
	for i, kind := range []string{task.EventCreated, task.EventLinked, builtin.EventClassified, task.VerbEventKind(task.VerbNext)} {
		evs = append(evs, port.Event{Unit: u, Kind: kind, Key: string(rune('a' + i)), At: testNow})
	}
	if err := sqlite.New(db).RecordEvents(ctx, evs...); err != nil {
		t.Fatal(err)
	}
	got, err := taskTimeline(db).Of(ctx, id, timeline.Filter{})
	if err != nil || len(got) != 1 || got[0].Kind != task.VerbEventKind(task.VerbNext) {
		t.Fatalf("the timeline %+v %v, want only the verb", got, err)
	}
}
