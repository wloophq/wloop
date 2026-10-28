package sqlite

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// putTask stores a task holding units.
func putTask(t *testing.T, db *DB, id task.ID, units ...unit.Unit) {
	t.Helper()
	when := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := db.Tasks().Apply(context.Background(), []task.Change{{Op: task.OpCreate, Task: task.Task{ID: id, Kind: task.KindAction,
		State: "done", Category: task.CategoryDone, Title: "Ship it", Origin: task.OriginUser, Units: units, Created: when, Updated: when}}}); err != nil {
		t.Fatal(err)
	}
}

func TestAnEndedPRWithATaskKeepsItsTurnsPlanItemsAndBriefs(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	worked, alone := pr.Ref{Owner: "acme", Repo: "api", Number: 1}, pr.Ref{Owner: "acme", Repo: "api", Number: 2}
	for i, ref := range []pr.Ref{worked, alone} {
		insertPR(t, db, ref, "MERGED", at(retainNow.Add(-401*day)), at(retainNow.Add(-401*day)))
		seedPRRows(t, db, ref, i+1)
	}
	putTask(t, db, "t-0000000000000001", worked.Unit())

	for range 2 {
		retain(t, db, DefaultRetention)
	}
	plugin := []string{"pull_requests", "pr_details", "pr_seen"}
	for table, n := range prRows(t, db, worked) {
		if !slices.Contains(plugin, table) && n != 1 {
			t.Errorf("%s keeps %d rows of the pull request a task works on, want 1", table, n)
		}
	}
	for table, n := range prRows(t, db, alone) {
		if n != 0 {
			t.Errorf("%s keeps %d rows of the pull request no task links", table, n)
		}
	}
}

func TestTaskEventsAreNeverRetained(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	own := task.UnitOf("t-0000000000000001")
	session := unit.Unit{Provider: unit.Session, Key: "term_1"}
	putTask(t, db, "t-0000000000000001", session)
	old := retainNow.Add(-500 * day)
	event_(t, db, string(own.Provider), own.Key, task.VerbEventKind(task.VerbDone), "", old)
	event_(t, db, string(own.Provider), own.Key, workflow.KindStepFailed, "w:1", old)
	event_(t, db, string(session.Provider), session.Key, SessionEventKinds[0], "term_1:a", old)
	event_(t, db, string(session.Provider), "term_2", SessionEventKinds[0], "term_2:a", old)
	event_(t, db, string(unit.Workspace), "ws-1", workflow.KindStepFailed, "w:2", old)
	exec(t, db, `INSERT INTO plan_items (id, workflow, provider, unit_key, key_value, decision, at, saved_at) VALUES ('w/t', 'w', 'task', ?, 'k', 'blocked', ?, ?)`,
		own.Key, at(old), at(old))

	got := retain(t, db, DefaultRetention)
	if got[SessionEventsKey] != 1 || got[WorkflowEventsKey] != 1 || got["plan_items"] != 0 {
		t.Errorf("retained %v, want only the session event and the workflow event of units no task holds", got)
	}
	want := []string{
		"session|term_1|" + SessionEventKinds[0] + "|term_1:a",
		"task|" + own.Key + "|verb_done|",
		"task|" + own.Key + "|" + workflow.KindStepFailed + "|w:1",
	}
	if left := remainingEvents(t, db); !slices.Equal(left, want) {
		t.Errorf("events left %q, want %q", left, want)
	}
	if n := countOf(t, db, `SELECT count(*) FROM plan_items WHERE provider = 'task'`); n != 1 {
		t.Errorf("%d plan items of the task, want 1", n)
	}
}
