package facttask_test

import (
	"testing"

	"github.com/wloophq/wloop/internal/domain/task"
)

func TestTaskFactsReadTheStoredCategory(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.task(task.Task{ID: "t-triage", State: "triage", Category: task.CategoryInbox, Title: "a"})
	e.task(task.Task{ID: "t-done", State: "done", Category: task.CategoryActive, Title: "b"})
	for id, want := range map[task.ID]struct{ state, category string }{"t-triage": {"triage", "inbox"}, "t-done": {"done", "active"}} {
		f := e.facts(id)
		if f["task.state"] != want.state || f["task.category"] != want.category {
			t.Errorf("%s: state %v category %v, want %s %s", id, f["task.state"], f["task.category"], want.state, want.category)
		}
	}
}
