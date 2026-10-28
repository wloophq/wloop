package task_test

import "github.com/wloophq/wloop/internal/domain/task"

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
