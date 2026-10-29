package facttask_test

import (
	"testing"
	"time"

	facttask "github.com/wloophq/wloop/internal/adapter/facts/task"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
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
