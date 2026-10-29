package facttask_test

import (
	"slices"
	"testing"
	"time"

	facttask "github.com/wloophq/wloop/internal/adapter/facts/task"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/turn"
)

func TestALinkSignalIsTheOrOfTheLinks(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.task(task.Task{ID: "t-a", State: "next", Category: task.CategoryReady, Title: "a", Units: []unit.Unit{chatA, chatB}})
	e.task(task.Task{ID: "t-b", State: "next", Category: task.CategoryReady, Title: "b"})
	e.links[chatA] = facttask.Link{Signals: map[string]bool{"stalled": true, "in_loop": false, "snoring": true}}
	e.links[chatB] = facttask.Link{Signals: map[string]bool{"in_loop": true, "ready": false}}
	if len(builtin.LinkSignals) == 0 {
		t.Fatal("no plugin declares a link signal")
	}
	a, b := e.facts("t-a"), e.facts("t-b")
	for _, name := range builtin.LinkSignals {
		fact := builtin.LinkSignalPrefix + name
		if want := name == "stalled" || name == "in_loop"; a[fact] != want {
			t.Errorf("%s of t-a = %v, want %v", fact, a[fact], want)
		}
		if b[fact] != false {
			t.Errorf("%s of t-b, which links nothing = %v", fact, b[fact])
		}
	}
	if _, ok := a[builtin.LinkSignalPrefix+"snoring"]; ok {
		t.Error("a signal no plugin declares became a fact")
	}
}

func TestSinceMovementIsTheNewestMoveOfTheTaskOrItsLinks(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.task(task.Task{ID: "t-a", State: "next", Category: task.CategoryReady, Title: "a", Units: []unit.Unit{chatA, chatB},
		Created: monday.Add(-48 * time.Hour), Updated: monday.Add(-24 * time.Hour)})
	if got := e.facts("t-a")["task.since_movement"]; got != 24*time.Hour {
		t.Errorf("with links that never moved: %v, want the task's own last change 24h ago", got)
	}
	e.links[chatA] = facttask.Link{Moved: monday.Add(-3 * time.Hour)}
	e.links[chatB] = facttask.Link{Moved: monday.Add(-30 * time.Hour)}
	if got := e.facts("t-a")["task.since_movement"]; got != 3*time.Hour {
		t.Errorf("since_movement = %v, want 3h, the newest link's move", got)
	}
}

// autofocus is whether the bundled task-autofocus workflow stars a task whose facts are f.
func autofocus(t *testing.T, f workflow.Facts) bool {
	t.Helper()
	l, err := turn.Bundled()
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(l.Workflows, func(w workflow.Workflow) bool { return w.Name == "task-autofocus" })
	if i < 0 {
		t.Fatal("no task-autofocus")
	}
	ok, err := l.Workflows[i].When.Match(workflow.Vars{Facts: f, Now: monday})
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func TestASourceThatReportsSignalsKeepsItsOwnStaleness(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ana := items.Person{Key: "chat:ANA"}
	e.task(task.Task{ID: "t-pr", State: "waiting", Category: task.CategoryWaiting, Title: "pr", Units: []unit.Unit{chatA},
		Items: []task.ItemRef{{Unit: chatA, Item: "i-pr"}}})
	e.item(chatA, "i-pr", items.Open, monday.Add(-10*24*time.Hour), ana)
	e.task(task.Task{ID: "t-thread", State: "waiting", Category: task.CategoryWaiting, Title: "thread", Units: []unit.Unit{chatB},
		Items: []task.ItemRef{{Unit: chatB, Item: "i-th"}}})
	e.item(chatB, "i-th", items.Open, monday.Add(-10*24*time.Hour), ana)
	e.links[chatA] = facttask.Link{Signals: map[string]bool{"stalled": false, "in_loop": false, "ready": false}}

	pr, thread := e.facts("t-pr"), e.facts("t-thread")
	if got, _ := pr["task.link_signals"].([]string); !slices.Equal(got, []string{"in_loop", "ready", "stalled"}) {
		t.Errorf("link_signals of t-pr %v", pr["task.link_signals"])
	}
	if got, _ := thread["task.link_signals"].([]string); len(got) != 0 {
		t.Errorf("link_signals of t-thread %v", got)
	}
	if at, _ := pr["task.check_on"].(time.Time); !at.IsZero() {
		t.Errorf("t-pr, whose source reports its own staleness, checks on %v", at)
	}
	if at, _ := thread["task.check_on"].(time.Time); at.IsZero() || !at.Before(monday) {
		t.Errorf("t-thread checks on %v, want the fallback, past", at)
	}
	if autofocus(t, pr) {
		t.Error("task-autofocus stars a quiet pull request someone else owes")
	}
	if !autofocus(t, thread) {
		t.Error("task-autofocus leaves a quiet thread someone else owes unstarred")
	}
}
