package task_test

import (
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
)

// bundled are the bundled profile's states, in its order.
type bundledStates struct{}

var bundled = bundledStates{}

var bundledTable = []struct {
	name string
	c    task.Category
}{
	{"inbox", task.CategoryInbox}, {"next", task.CategoryReady}, {"waiting", task.CategoryWaiting},
	{"scheduled", task.CategoryDeferred}, {"someday", task.CategoryDeferred}, {"done", task.CategoryDone},
	{"reference", task.CategoryDone}, {"trash", task.CategoryDropped},
}

func (bundledStates) CategoryOf(s string) (task.Category, bool) {
	for _, r := range bundledTable {
		if r.name == s {
			return r.c, true
		}
	}
	return "", false
}

func (bundledStates) FirstOf(c task.Category) string {
	for _, r := range bundledTable {
		if r.c == c {
			return r.name
		}
	}
	return ""
}

// PropOf is the bundled property each role acts on.
func (bundledStates) PropOf(r task.Role) (string, []string) {
	switch r {
	case task.RoleArea:
		return "area", nil
	case task.RoleContexts:
		return "contexts", nil
	case task.RoleStar:
		return "focus", []string{"on", "off"}
	case task.RoleDeadline:
		return "due", nil
	}
	return "", nil
}

func must[T any](t *testing.T, v T, err error) T {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func reconciled(t *testing.T, f task.UnitFacts, its []items.Item, me task.Me, have []task.Task, st task.Profile, now time.Time) []task.Change {
	t.Helper()
	cs, err := task.Reconcile(f, its, me, have, st, now)
	return must(t, cs, err)
}

func activity(f task.UnitFacts, have []task.Task, now time.Time) []task.Change {
	return task.Activity(f, have, now)
}
