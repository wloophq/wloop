package daemon

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/domain/task"
)

func relateBody(other string) string {
	return fmt.Sprintf(`{"source":"cockpit","args":{"task":%q}}`, other)
}

func (r *tasksRig) view(t *testing.T, id string) api.TaskView {
	t.Helper()
	var v api.TaskView
	if code := get(t, r.s, api.TaskPath(id), &v); code != 200 {
		t.Fatalf("view of %s: %d", id, code)
	}
	return v
}

func TestATaskViewListsItsRelatedTasks(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.put(t, task.Task{ID: "t-1", Title: "Draft the launch post", State: "next", Category: task.CategoryReady},
		task.Task{ID: "t-2", Title: "Book the launch venue", State: "inbox", Category: task.CategoryInbox},
		task.Task{ID: "t-3", Title: "Order the badges", State: "next", Category: task.CategoryReady})
	if st := verbState(r.view(t, "t-1"), "relate"); !st.Enabled {
		t.Fatalf("relate on t-1: %+v", st)
	}
	for _, other := range []string{"t-2", "t-3"} {
		if rec := post(t, r.s, api.TaskPath("t-1", "relate"), relateBody(other)); rec.Code != 200 {
			t.Fatalf("relate %s: %d %s", other, rec.Code, rec.Body)
		}
	}
	want := []api.TaskRef{{ID: "t-2", Title: "Book the launch venue", State: "inbox"}, {ID: "t-3", Title: "Order the badges", State: "next"}}
	if got := r.view(t, "t-1").Related; !reflect.DeepEqual(got, want) {
		t.Errorf("t-1 related %+v, want %+v", got, want)
	}
	if got := r.view(t, "t-2").Related; !reflect.DeepEqual(got, []api.TaskRef{{ID: "t-1", Title: "Draft the launch post", State: "next"}}) {
		t.Errorf("t-2 related %+v", got)
	}
	e, err := r.db.Tasks().Task(context.Background(), "t-1")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := catalog.PropFacts(e, bundledLoaded().Tasks)["task.prop.related"].([]string); !slices.Equal(got, []string{"t-2", "t-3"}) {
		t.Errorf("t-1's fact task.prop.related %v", got)
	}

	if rec := post(t, r.s, api.TaskPath("t-3", "unrelate"), relateBody("t-1")); rec.Code != 200 {
		t.Fatalf("unrelate: %d %s", rec.Code, rec.Body)
	}
	if got := r.view(t, "t-1").Related; !reflect.DeepEqual(got, want[:1]) {
		t.Errorf("t-1 related %+v after unrelate", got)
	}
	if got := r.view(t, "t-3").Related; len(got) != 0 {
		t.Errorf("t-3 related %+v after unrelate", got)
	}
}

func TestARelatedTaskMergedElsewhereReadsAsMerged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newTasksRig(t, nil)
	r.put(t, task.Task{ID: "t-1", Title: "Draft the launch post", State: "next", Category: task.CategoryReady},
		task.Task{ID: "t-2", Title: "Book the venue", State: "inbox", Category: task.CategoryInbox},
		task.Task{ID: "t-3", Title: "Book the launch venue", State: "next", Category: task.CategoryReady})
	if rec := post(t, r.s, api.TaskPath("t-1", "relate"), relateBody("t-2")); rec.Code != 200 {
		t.Fatalf("relate: %d %s", rec.Code, rec.Body)
	}
	sqlitetest.Exec(t, r.db, `DELETE FROM tasks WHERE id = 't-2'`)
	sqlitetest.Exec(t, r.db, `INSERT INTO task_aliases (old_id, new_id) VALUES ('t-2', 't-3')`)

	merged := []api.TaskRef{{ID: "t-2", Title: "Book the launch venue", State: "next", Merged: true}}
	if got := r.view(t, "t-1").Related; !reflect.DeepEqual(got, merged) {
		t.Fatalf("t-1 related %+v, want %+v", got, merged)
	}
	stored := func(id task.ID) []string {
		t.Helper()
		e, err := r.db.Tasks().Task(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return e.Prop("related").List
	}
	if rec := post(t, r.s, api.TaskPath("t-1", "relate"), relateBody("t-3")); rec.Code != 200 {
		t.Fatalf("relate to the survivor: %d %s", rec.Code, rec.Body)
	}
	if got := stored("t-1"); !slices.Equal(got, []string{"t-2"}) {
		t.Errorf("relating the task t-2 reads as listed t-1 %v", got)
	}
	if got := stored("t-3"); !slices.Equal(got, []string{"t-1"}) {
		t.Errorf("the survivor lists %v, want t-1 once relate links it", got)
	}
	if rec := post(t, r.s, api.TaskPath("t-1", "unrelate"), relateBody("t-3")); rec.Code != 200 {
		t.Fatalf("unrelate: %d %s", rec.Code, rec.Body)
	}
	if got := r.view(t, "t-1").Related; len(got) != 0 {
		t.Errorf("t-1 related %+v after unrelating the task t-2 reads as", got)
	}
	if got := stored("t-3"); len(got) != 0 {
		t.Errorf("the survivor lists %v after unrelate", got)
	}
}

func TestAnAgentTheUserDoesNotDriveCannotRelate(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	svc := sessionsOn(r.db)
	r.s.deps.Turns = svc
	adoptSession(t, svc, "term_1", false)
	adoptSession(t, svc, "term_2", true)
	r.put(t, task.Task{ID: "t-1", State: "next", Category: task.CategoryReady},
		task.Task{ID: "t-2", State: "next", Category: task.CategoryReady})
	related := func() []api.TaskRef { return r.view(t, "t-2").Related }
	if rec := postFrom(t, r.s, "term_1", api.TaskPath("t-1", "relate"), relateBody("t-2")); rec.Code != 403 {
		t.Fatalf("a worker session's relate: %d %s", rec.Code, rec.Body)
	}
	if got := related(); len(got) != 0 {
		t.Fatalf("the refused relate linked %+v", got)
	}
	if rec := postFrom(t, r.s, "term_2", api.TaskPath("t-1", "relate"), relateBody("t-2")); rec.Code != 200 {
		t.Fatalf("the relate of a session the user drives: %d %s", rec.Code, rec.Body)
	}
	if rec := postFrom(t, r.s, "term_1", api.TaskPath("t-2", "unrelate"), relateBody("t-1")); rec.Code != 403 {
		t.Fatalf("a worker session's unrelate: %d %s", rec.Code, rec.Body)
	}
	if got := related(); len(got) != 1 || got[0].ID != "t-1" {
		t.Fatalf("t-2 related %+v after the refused unrelate", got)
	}
}

func TestARelatedListNamesEachTaskOnceAndNeverItself(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.put(t, task.Task{ID: "t-1", Title: "Draft the launch post", State: "inbox", Category: task.CategoryInbox,
		Props: task.Props{"related": {List: []string{"t-2", "t-3", "t-9", "t-404", "t-4"}}}},
		task.Task{ID: "t-3", Title: "Book the launch venue", State: "inbox", Category: task.CategoryInbox},
		task.Task{ID: "t-4", Title: "Order the badges", State: "next", Category: task.CategoryReady})
	sqlitetest.Exec(t, r.db, `INSERT INTO task_aliases (old_id, new_id) VALUES ('t-2', 't-3'), ('t-9', 't-1')`)
	want := []api.TaskRef{{ID: "t-2", Title: "Book the launch venue", State: "inbox", Merged: true}, {ID: "t-4", Title: "Order the badges", State: "next"}}
	if got := r.view(t, "t-1").Related; !reflect.DeepEqual(got, want) {
		t.Errorf("the detail of t-1 lists %+v, want %+v", got, want)
	}
	i := slices.IndexFunc(r.page(t, "view=inbox").Tasks, func(v api.TaskView) bool { return v.ID == "t-1" })
	if i < 0 {
		t.Fatal("t-1 is not in the inbox")
	}
	if got := r.page(t, "view=inbox").Tasks[i].Related; !reflect.DeepEqual(got, want) {
		t.Errorf("the inbox row of t-1 lists %+v, want %+v", got, want)
	}
}
