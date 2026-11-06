package daemon

import (
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/task"
)

func TestTasksMatchListsTheOpenTasksWhoseTitleHoldsIt(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	at := func(h int) time.Time { return tasksNow.Add(time.Duration(-h) * time.Hour) }
	r.put(t, task.Task{ID: "t-1", Title: "Draft the launch post", State: "next", Category: task.CategoryReady, Created: at(9), Updated: at(3)},
		task.Task{ID: "t-2", Title: "Book the LAUNCH venue", State: "inbox", Category: task.CategoryInbox, Created: at(9), Updated: at(1)},
		task.Task{ID: "t-3", Title: "Launch retro", State: "done", Category: task.CategoryDone, Created: at(9), Updated: at(1)},
		task.Task{ID: "t-4", Title: "Order the badges", State: "someday", Category: task.CategoryDeferred, Created: at(9), Updated: at(2)},
		task.Task{ID: "draft:7", Title: "Reply on the launch thread", State: "outbox", Category: task.CategoryInbox, Origin: task.OriginDraft, Created: at(9), Updated: at(1)},
		task.Task{ID: "t-5", Title: "Launch checklist", State: "waiting", Category: task.CategoryWaiting, Created: at(9), Updated: at(3)})

	got := r.page(t, api.TasksMatch+"=launch")
	want := []api.TaskView{{ID: "t-2", Title: "Book the LAUNCH venue", State: "inbox", Category: "inbox"},
		{ID: "t-1", Title: "Draft the launch post", State: "next", Category: "ready"},
		{ID: "t-5", Title: "Launch checklist", State: "waiting", Category: "waiting"}}
	if !got.Full || !reflect.DeepEqual(picked(got.Tasks), want) {
		t.Errorf("match launch: full %v, %+v, want %+v", got.Full, picked(got.Tasks), want)
	}
	if got := taskIDs(r.page(t, "view=inbox&"+api.TasksMatch+"="+url.QueryEscape(" LAUNCH ")).Tasks); !reflect.DeepEqual(got, []string{"t-2", "t-1", "t-5"}) {
		t.Errorf("a match ignores the view and the spaces around it: %v", got)
	}
	if got := taskIDs(r.page(t, api.TasksMatch+"="+url.QueryEscape("the launch p")).Tasks); !reflect.DeepEqual(got, []string{"t-1"}) {
		t.Errorf("a match holds its words in order: %v", got)
	}
	if got := taskIDs(r.page(t, api.TasksMatch+"=").Tasks); !reflect.DeepEqual(got, []string{"t-2", "t-4", "t-1", "t-5"}) {
		t.Errorf("an empty match lists every open task, the newest change first: %v", got)
	}
}

// picked are the fields a picker reads of each view.
func picked(vs []api.TaskView) []api.TaskView {
	out := []api.TaskView{}
	for _, v := range vs {
		out = append(out, api.TaskView{ID: v.ID, Title: v.Title, State: v.State, Category: v.Category})
	}
	return out
}
