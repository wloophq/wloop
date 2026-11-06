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

func TestTheSurvivorsDetailReadsAMergedTasksBriefWhenItHasNone(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.s.deps.TaskRecords = r.db
	ctx := context.Background()
	const survivor, merged = task.ID("t-1111111111111111"), task.ID("t-2222222222222222")
	r.put(t, task.Task{ID: survivor, State: "next", Category: task.CategoryReady, Title: "Ship the export"})
	if err := r.db.PutRecord(ctx, task.UnitOf(merged), "brief", task.WriterPlugin, []byte(`{"goal":"Export the ledger"}`), tasksNow); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Write(ctx, func(tx *sqlite.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO task_aliases (old_id, new_id) VALUES (?, ?)`, string(merged), string(survivor))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := sqlite.New(r.db).RecordEvents(ctx, port.Event{Unit: task.UnitOf(merged), Kind: merge.RecordKind, Actor: "person",
		Data: `{"Into":"` + string(survivor) + `"}`, At: tasksNow}); err != nil {
		t.Fatal(err)
	}

	var got api.TaskView
	if code := get(t, r.s, api.TaskPath(string(survivor)), &got); code != 200 {
		t.Fatalf("GET the survivor: %d", code)
	}
	if got.Brief == nil || string(got.Brief.Data) != `{"goal":"Export the ledger"}` {
		t.Fatalf("the survivor with no brief of its own shows %+v, want the merged task's", got.Brief)
	}
	if err := r.db.PutRecord(ctx, task.UnitOf(survivor), "brief", task.WriterPlugin, []byte(`{"goal":"Ship it"}`), tasksNow); err != nil {
		t.Fatal(err)
	}
	if get(t, r.s, api.TaskPath(string(survivor)), &got); got.Brief == nil || string(got.Brief.Data) != `{"goal":"Ship it"}` {
		t.Fatalf("the survivor's own brief does not win: %+v", got.Brief)
	}
}

func TestASurvivorsBriefFallsBackToTheNewestMergedTaskFirst(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.s.deps.TaskRecords = r.db
	ctx := context.Background()
	const first, middle, survivor = task.ID("t-3333333333333333"), task.ID("t-4444444444444444"), task.ID("t-5555555555555555")
	r.put(t, task.Task{ID: survivor, State: "next", Category: task.CategoryReady, Title: "Ship the export"})
	for _, m := range []struct {
		from, into task.ID
		goal       string
	}{{first, middle, "Export March"}, {middle, survivor, "Export the quarter"}} {
		if err := r.db.PutRecord(ctx, task.UnitOf(m.from), "brief", task.WriterPlugin, []byte(`{"goal":"`+m.goal+`"}`), tasksNow); err != nil {
			t.Fatal(err)
		}
		if _, err := r.db.Write(ctx, func(tx *sqlite.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO task_aliases (old_id, new_id) VALUES (?, ?)`, string(m.from), string(m.into))
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if err := sqlite.New(r.db).RecordEvents(ctx, port.Event{Unit: task.UnitOf(m.from), Kind: merge.RecordKind, Actor: "person",
			Data: `{"Into":"` + string(m.into) + `"}`, At: tasksNow}); err != nil {
			t.Fatal(err)
		}
	}
	var got api.TaskView
	if code := get(t, r.s, api.TaskPath(string(survivor)), &got); code != 200 {
		t.Fatalf("GET the survivor: %d", code)
	}
	if got.Brief == nil || string(got.Brief.Data) != `{"goal":"Export the quarter"}` {
		t.Fatalf("the survivor shows %+v, want the newest merged task's brief", got.Brief)
	}
}
