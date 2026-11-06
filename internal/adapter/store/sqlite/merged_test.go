package sqlite

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/timeline"
	"github.com/wloophq/wloop/internal/domain/merge"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// mergedAway leaves task from as a merge leaves it: no row, an alias to into, and its merge record on its own
// unit at when.
func mergedAway(t *testing.T, db *DB, from, into task.ID, when time.Time) {
	t.Helper()
	exec(t, db, `DELETE FROM tasks WHERE id = ?`, string(from))
	exec(t, db, `INSERT INTO task_aliases (old_id, new_id) VALUES (?, ?)`, string(from), string(into))
	exec(t, db, `INSERT INTO events (provider, unit_key, kind, key, actor, at, data) VALUES ('task', ?, ?, '', 'person', ?, '{}')`,
		string(from), merge.RecordKind, at(when))
}

func mergedIDs(t *testing.T, db *DB, id task.ID) []task.ID {
	t.Helper()
	got, err := db.MergedInto(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestMergedIntoWalksTheAliasesTransitively(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	const survivor, middle, first, beside, split, stale = "t-survivor", "t-middle", "t-first", "t-beside", "t-split", "t-stale"
	for _, id := range []task.ID{survivor, middle, first, beside, split, stale} {
		putTask(t, db, id)
	}
	day := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	mergedAway(t, db, first, middle, day)
	mergedAway(t, db, middle, survivor, day.Add(time.Hour))
	mergedAway(t, db, beside, survivor, day.Add(2*time.Hour))
	exec(t, db, `INSERT INTO task_aliases (old_id, new_id) VALUES (?, ?)`, split, survivor)
	exec(t, db, `DELETE FROM tasks WHERE id = ?`, stale)
	exec(t, db, `INSERT INTO task_aliases (old_id, new_id) VALUES (?, ?)`, stale, first)
	exec(t, db, `INSERT INTO task_aliases (old_id, new_id) VALUES (?, ?)`, middle, stale)

	if got, want := mergedIDs(t, db, survivor), []task.ID{stale, first, middle, beside}; !slices.Equal(got, want) {
		t.Fatalf("merged into the survivor %q, want %q: transitive, in merge order, without the stored task", got, want)
	}
	if got := mergedIDs(t, db, split); len(got) != 0 {
		t.Fatalf("merged into a task none merged into %q", got)
	}
}

func TestUnitsOfIncludesTheOwnUnitOfEveryMergedTask(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	ctx := context.Background()
	thread := unit.Unit{Provider: "slack", Key: "T000/C000/1.2"}
	pr := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	const survivor, merged, deeper = "t-survivor", "t-merged", "t-deeper"
	putTask(t, db, survivor, thread, pr)
	putTask(t, db, merged)
	putTask(t, db, deeper)
	day := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	mergedAway(t, db, deeper, merged, day)
	mergedAway(t, db, merged, survivor, day.Add(time.Hour))

	got, err := db.UnitsOf(ctx, survivor)
	if err != nil {
		t.Fatal(err)
	}
	if want := []unit.Unit{thread, pr, task.UnitOf(deeper), task.UnitOf(merged)}; !slices.Equal(got, want) {
		t.Fatalf("units of the survivor %v, want %v", got, want)
	}

	event_(t, db, "task", merged, "message", "", day.Add(-time.Hour))
	event_(t, db, "task", deeper, task.VerbEventKind(task.VerbDone), "", day.Add(-2*time.Hour))
	evs, err := timeline.Merged{Log: db, Holdings: db}.Of(ctx, survivor, timeline.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, ev := range evs {
		kinds = append(kinds, ev.Kind+" "+ev.Unit.Key)
	}
	if want := []string{"verb_done " + deeper, "message " + merged}; !slices.Equal(kinds, want) {
		t.Fatalf("the survivor's timeline %q, want %q, the merge records left out", kinds, want)
	}

	exec(t, db, `DELETE FROM task_aliases WHERE old_id = ?`, merged)
	putTask(t, db, merged)
	if got, _ := db.UnitsOf(ctx, survivor); !slices.Equal(got, []unit.Unit{thread, pr}) {
		t.Fatalf("units of the survivor after the split %v", got)
	}
	if got, _ := db.UnitsOf(ctx, merged); !slices.Equal(got, []unit.Unit{task.UnitOf(deeper)}) {
		t.Fatalf("units of the task split back %v, want its own merged task", got)
	}
}

func TestRetentionKeepsTheMergeRecordAndTheMergedTasksHistory(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	ctx := context.Background()
	const survivor, merged = "t-survivor", "t-merged"
	putTask(t, db, survivor)
	putTask(t, db, merged)
	old := retainNow.Add(-500 * day)
	mergedAway(t, db, merged, survivor, old)
	event_(t, db, "task", merged, "message", "", old)
	event_(t, db, "task", merged, task.VerbEventKind(task.VerbDone), "", old)

	for range 2 {
		retain(t, db, DefaultRetention)
	}
	want := []string{
		"task|" + merged + "|" + merge.RecordKind + "|",
		"task|" + merged + "|message|",
		"task|" + merged + "|verb_done|",
	}
	if left := remainingEvents(t, db); !slices.Equal(left, want) {
		t.Fatalf("events left %q, want %q", left, want)
	}
	if got := mergedIDs(t, db, survivor); !slices.Equal(got, []task.ID{merged}) {
		t.Fatalf("merged into the survivor after retention %q", got)
	}
	evs, err := timeline.Merged{Log: db, Holdings: db}.Of(ctx, survivor, timeline.Filter{})
	if err != nil || len(evs) != 2 {
		t.Fatalf("the survivor's timeline after retention %+v %v", evs, err)
	}
}
