package daemon

import (
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/task"
)

func TestTheTaskRouteFollowsAnOldIDToTheTaskThatTookItOver(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.put(t, task.Task{ID: "t-0000000000000002", State: "next", Category: task.CategoryReady, Title: "Answer Ana"})
	sqlitetest.Exec(t, r.db, `INSERT INTO task_aliases (old_id, new_id) VALUES ('t-0000000000000001', 't-0000000000000002')`)
	var got api.TaskView
	if code := get(t, r.s, api.TaskPath("t-0000000000000001"), &got); code != 200 {
		t.Fatalf("GET the old id: %d", code)
	}
	if got.ID != "t-0000000000000002" || got.Title != "Answer Ana" {
		t.Errorf("the old id served %+v, want the task that took it over", got)
	}
	if code := get(t, r.s, api.TaskPath("t-0000000000000003"), &got); code != 404 {
		t.Errorf("GET an id no task or alias names: %d, want 404", code)
	}
}
