package task_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
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

func TestMovingIntoACategoryWithNoStateIsAnError(t *testing.T) {
	st := userStates{"triage": task.CategoryInbox}
	e := task.Task{State: "triage", Category: task.CategoryInbox}
	err := e.MoveTo(task.CategoryWaiting, st)
	if !errors.Is(err, task.ErrNoStateOf) || !strings.Contains(err.Error(), "waiting") {
		t.Fatalf("err %v", err)
	}
	if e.State != "triage" || e.Category != task.CategoryInbox {
		t.Fatalf("a refused move changed the task: %+v", e)
	}
	f := task.UnitFacts{Unit: unit.Unit{Provider: "github", Key: "acme/api#7"}, Mute: &task.Mute{}}
	it := items.Item{ID: "i1", Unit: f.Unit, State: "open", Summary: "s", WaitingOn: []items.Person{{Key: "github:me"}}}
	if _, err := task.Reconcile(f, []items.Item{it}, task.Me{Keys: []string{"github:me"}}, nil, st, now); !errors.Is(err, task.ErrNoStateOf) {
		t.Fatalf("a mute into a profile with no deferred state: %v", err)
	}
}
