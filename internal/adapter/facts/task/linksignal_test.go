package facttask_test

import (
	"maps"
	"slices"
	"testing"
	"time"

	facttask "github.com/wloophq/wloop/internal/adapter/facts/task"
	"github.com/wloophq/wloop/internal/catalog"
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

// stalls is whether the bundled stalled view holds a task whose facts are f.
func stalls(t *testing.T, f workflow.Facts) bool {
	t.Helper()
	l, err := turn.Bundled()
	if err != nil {
		t.Fatal(err)
	}
	in, err := catalog.ViewAt(l.Views, "stalled", task.Task{}, f, monday)
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func TestASourceThatReportsSignalsKeepsItsOwnStaleness(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ana := items.Person{Key: "chat:ANA"}
	mailY := unit.Unit{Provider: "chat", Key: "T1/C1/300.3"}
	for id, u := range map[task.ID]unit.Unit{"t-pr": chatA, "t-thread": chatB, "t-loop": mailY} {
		e.task(task.Task{ID: id, State: "waiting", Category: task.CategoryWaiting, Title: string(id), Units: []unit.Unit{u},
			Items: []task.ItemRef{{Unit: u, Item: "i-" + string(id)}}, Created: monday.Add(-10 * 24 * time.Hour), Updated: monday.Add(-10 * 24 * time.Hour)})
		e.item(u, "i-"+string(id), items.Open, monday.Add(-10*24*time.Hour), ana)
	}
	e.links[chatA] = facttask.Link{Signals: map[string]bool{"stalled": false, "in_loop": false, "ready": false}}
	e.links[mailY] = facttask.Link{Signals: map[string]bool{"in_loop": false}}

	pr, thread, loop := e.facts("t-pr"), e.facts("t-thread"), e.facts("t-loop")
	for name, c := range map[string]struct {
		f       workflow.Facts
		signals []string
		stalls  bool
	}{
		"a pull request whose source says it has not stalled":        {pr, []string{"in_loop", "ready", "stalled"}, false},
		"a quiet thread whose source reports no signal":              {thread, []string{}, true},
		"a quiet task whose source reports in_loop and no staleness": {loop, []string{"in_loop"}, true},
	} {
		if got, _ := c.f["task.link_signals"].([]string); !slices.Equal(got, c.signals) {
			t.Errorf("%s: link_signals %v, want %v", name, got, c.signals)
		}
		if at, _ := c.f["task.check_on"].(time.Time); at.IsZero() || !at.Before(monday) {
			t.Errorf("%s: checks on %v, want the fallback, past", name, at)
		}
		if got := stalls(t, c.f); got != c.stalls {
			t.Errorf("%s: in Stalled %v, want %v", name, got, c.stalls)
		}
		if got := autofocus(t, c.f); got != c.stalls {
			t.Errorf("%s: task-autofocus stars it %v, want %v", name, got, c.stalls)
		}
	}
	pr["task.link_signal.stalled"] = true
	if !stalls(t, pr) {
		t.Error("a pull request whose source says it stalled is not in Stalled")
	}
}

func TestPrTaskApprovedClosesATaskWhoseLinkYouApprovedAndAsksNothingOfYou(t *testing.T) {
	t.Parallel()
	l, err := turn.Bundled()
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(l.Workflows, func(w workflow.Workflow) bool { return w.Name == "pr-task-approved" })
	if i < 0 {
		t.Fatal("no pr-task-approved")
	}
	e := newEnv(t)
	e.task(task.Task{ID: "t-a", State: "next", Category: task.CategoryReady, Title: "a", Units: []unit.Unit{chatA}})
	e.links[chatA] = facttask.Link{Signals: map[string]bool{"approved_by_me": true}}
	approved := e.facts("t-a")
	approved["task.prop.kind"] = "pull-request"
	for name, c := range map[string]struct {
		edit func(workflow.Facts)
		want bool
	}{
		"approved, nothing owed": {func(workflow.Facts) {}, true},
		"not approved":           {func(f workflow.Facts) { f[builtin.LinkSignalPrefix+"approved_by_me"] = false }, false},
		"a review asked of me":   {func(f workflow.Facts) { f["task.link_owes"] = []string{"me"} }, false},
		"a question open":        {func(f workflow.Facts) { f["task.gates_open"] = 1 }, false},
		"not a pull request":     {func(f workflow.Facts) { f["task.prop.kind"] = "" }, false},
		"already done":           {func(f workflow.Facts) { f["task.category"] = string(task.CategoryDone) }, false},
	} {
		f := maps.Clone(approved)
		c.edit(f)
		got, err := l.Workflows[i].When.Match(workflow.Vars{Facts: f, Now: monday})
		if err != nil || got != c.want {
			t.Errorf("%s: %v, %v; want %v", name, got, err, c.want)
		}
	}
	if !slices.Equal(l.Workflows[i].Key, []string{"task.units"}) {
		t.Errorf("key = %v: a task you open again after it closed must stay open", l.Workflows[i].Key)
	}
}
