package task_test

import (
	"testing"

	"github.com/wloophq/wloop/internal/domain/task"
)

type userStates map[string]task.Category

func (u userStates) CategoryOf(s string) (task.Category, bool) { c, ok := u[s]; return c, ok }
func (u userStates) FirstOf(c task.Category) string {
	for _, s := range []string{"triage", "done", "archived"} {
		if u[s] == c {
			return s
		}
	}
	return ""
}

func TestOpenMeansACategoryNotAName(t *testing.T) {
	st := userStates{"triage": task.CategoryInbox, "done": task.CategoryReady, "archived": task.CategoryDone}
	var e task.Task
	e.Become("done", st)
	if !e.Open() || e.Category != task.CategoryReady {
		t.Fatalf("a state named done whose category is ready: open %v, category %s", e.Open(), e.Category)
	}
	e.Become("archived", st)
	if e.Open() {
		t.Fatal("a state of the done category is open")
	}
	e.MoveTo(task.CategoryInbox, st)
	if e.State != "triage" || !e.Open() {
		t.Fatalf("moved to the inbox: %+v", e)
	}
	if !(task.Task{Category: task.CategoryWaiting, State: "trash"}).Open() || (task.Task{Category: task.CategoryDropped, State: "next"}).Open() {
		t.Fatal("Open read the state's name")
	}
}
