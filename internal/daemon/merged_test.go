package daemon

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/timeline"
	"github.com/wloophq/wloop/internal/domain/merge"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestTheSurvivorsTimelineRouteServesTheMergedTasksHistory(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.s.deps.Timeline = timeline.Merged{Log: r.db, Holdings: r.db}
	ctx := context.Background()
	thread := unit.Unit{Provider: "slack", Key: "T000/C000/1.2"}
	const survivor, merged = task.ID("t-1111111111111111"), task.ID("t-2222222222222222")
	r.put(t, task.Task{ID: survivor, State: "next", Category: task.CategoryReady, Title: "Ship the export", Units: []unit.Unit{thread}})
	r.put(t, task.Task{ID: merged, State: "next", Category: task.CategoryReady, Title: "Export the ledger"})
	own := task.UnitOf(merged)
	at := func(i int) time.Time { return tasksNow.Add(time.Duration(i) * time.Minute) }
	if err := sqlite.New(r.db).RecordEvents(ctx,
		port.Event{Unit: own, Kind: "message", Actor: "person", Data: `{"text":"Start with March"}`, At: at(1)},
		port.Event{Unit: own, Kind: task.VerbEventKind(task.VerbFocus), Actor: "person:cockpit", At: at(2)},
		port.Event{Unit: task.UnitOf(survivor), Kind: "message", Actor: "person", Data: `{"text":"Ship it Friday"}`, At: at(3)},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Write(ctx, func(tx *sqlite.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM tasks WHERE id = ?`, string(merged)); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO task_aliases (old_id, new_id) VALUES (?, ?)`, string(merged), string(survivor))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := sqlite.New(r.db).RecordEvents(ctx, port.Event{Unit: own, Kind: merge.RecordKind, Actor: "person", Data: `{"Into":"` + string(survivor) + `"}`, At: at(4)}); err != nil {
		t.Fatal(err)
	}

	for _, id := range []task.ID{survivor, merged} {
		var evs []api.TimelineEvent
		if code := get(t, r.s, api.TaskPath(string(id), "timeline"), &evs); code != 200 {
			t.Fatalf("GET the timeline of %s: %d", id, code)
		}
		var got []string
		for _, ev := range evs {
			got = append(got, ev.Kind+"@"+ev.Unit)
		}
		want := []string{"message@", "verb_focus@" + own.String(), "message@" + own.String()}
		if !slices.Equal(got, want) {
			t.Errorf("the timeline of %s %q, want the survivor's with the merged task's history and no merge record %q", id, got, want)
		}
	}
}
