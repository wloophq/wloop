package task_test

import (
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var (
	askThread = unit.Unit{Provider: "slack", Key: "T1/C1/1.1"}
	askDM     = unit.Unit{Provider: "slack", Key: "T1/D9/2.2"}
	prTaskID  = task.DerivedID(pr, pr.String())
)

func slackItem(u unit.Unit, id string, mod ...func(*items.Item)) items.Item {
	it := item(id, mod...)
	it.Unit = u
	return it
}

func ref(u unit.Unit, id string) task.ItemRef { return task.ItemRef{Unit: u, Item: id} }

func onUnit(u unit.Unit, mod ...func(*task.UnitFacts)) task.UnitFacts {
	f := facts(mod...)
	f.Unit = u
	return f
}

func followed(f *task.UnitFacts)   { f.Cares, f.Carded = true, true }
func unfollowed(f *task.UnitFacts) { f.Cares, f.Carded = false, true }

// survivor is a task that took in mergedID, holding items and units as a merge left them.
func survivor(id task.ID, mergedID task.ID, units []unit.Unit, its, res []task.ItemRef) task.Task {
	e := task.Task{ID: id, State: "next", Category: task.CategoryReady, Title: "Finish acme/api#7", Units: units, Items: its,
		Resolved: res, Origin: task.OriginDerived, Created: now.Add(-48 * time.Hour), Updated: now.Add(-time.Hour),
		OpenChanged: now.Add(-48 * time.Hour)}
	e.SetProp("merged", task.Value{List: []string{string(mergedID)}}, task.WriterWorkflow, now.Add(-time.Hour))
	return e
}

func TestAMergedTaskKeepsItsOtherItemsWhenAMovedUnitsItemsResolve(t *testing.T) {
	dmTask := task.DerivedID(askDM, "s:dm")
	y := survivor(task.DerivedID(askThread, "s:ask"), dmTask, []unit.Unit{askThread, askDM},
		[]task.ItemRef{ref(askDM, "s:dm"), ref(askThread, "s:ask")}, nil)
	e := only(t, reconcile(t, onUnit(askDM, followed), []items.Item{slackItem(askDM, "s:dm", resolved)}, []task.Task{y}, now))
	if !e.Open() || !slices.Equal(e.Items, []task.ItemRef{ref(askThread, "s:ask")}) || !slices.Equal(e.Resolved, []task.ItemRef{ref(askDM, "s:dm")}) {
		t.Fatalf("the thread's ask still calls for the task: %+v", e)
	}
	e = only(t, reconcile(t, onUnit(askThread, followed), []items.Item{slackItem(askThread, "s:ask", resolved)}, []task.Task{e}, now))
	if e.Category != task.CategoryDone || len(e.Items) != 0 || len(e.Resolved) != 2 {
		t.Fatalf("no item of any unit calls for it: %+v", e)
	}
}

func TestAMergedTaskIsNotDroppedWhenAMovedUnitIsUnfollowed(t *testing.T) {
	y := survivor(task.DerivedID(askThread, "s:ask"), prTaskID, []unit.Unit{askThread, pr},
		[]task.ItemRef{ref(askThread, "s:ask"), ref(pr, "gh:review")}, nil)
	e := only(t, reconcile(t, onUnit(pr, unfollowed), []items.Item{item("gh:review", keyed, resolved)}, []task.Task{y}, now))
	if !e.Open() || !slices.Equal(e.Items, []task.ItemRef{ref(askThread, "s:ask")}) || !e.Prop(task.PropUnfollowed).Time.IsZero() {
		t.Fatalf("the moved pull request, unfollowed, dropped the thread's ask: %+v", e)
	}
}

func TestAMergedOwnTaskIsNotDroppedWhileAMovedUnitsItemsCall(t *testing.T) {
	y := survivor(prTaskID, task.DerivedID(askThread, "s:ask"), []unit.Unit{pr, askThread},
		[]task.ItemRef{ref(askThread, "s:ask"), ref(pr, "gh:review")}, nil)
	e := only(t, reconcile(t, onUnit(pr, unfollowed), []items.Item{item("gh:review", keyed, resolved)}, []task.Task{y}, now))
	if !e.Open() || !slices.Equal(e.Items, []task.ItemRef{ref(askThread, "s:ask")}) {
		t.Fatalf("the pull request's own task, unfollowed, dropped the thread's ask: %+v", e)
	}
	e = only(t, reconcile(t, onUnit(pr, unfollowed), []items.Item{item("gh:review", keyed, resolved)},
		[]task.Task{survivor(prTaskID, "t-00000000000000s1", []unit.Unit{pr, askThread}, []task.ItemRef{ref(pr, "gh:review")}, nil)}, now))
	if e.Category != task.CategoryDropped {
		t.Fatalf("an unfollowed own task holding nothing else: %+v", e)
	}
}

func TestAMergedOwnTaskResolvesWhenNoItemOfAnyUnitCalls(t *testing.T) {
	settled := []items.Item{item("gh:review", keyed, resolved, func(it *items.Item) { it.UpdatedAt = now.Add(-time.Minute) })}
	y := survivor(prTaskID, task.DerivedID(askThread, "s:ask"), []unit.Unit{pr, askThread},
		[]task.ItemRef{ref(pr, "gh:review")}, []task.ItemRef{ref(askThread, "s:ask")})
	if e := only(t, reconcile(t, onUnit(pr, followed), settled, []task.Task{y}, now)); e.Category != task.CategoryDone {
		t.Fatalf("a merged own task with nothing left open: %+v", e)
	}
	y.Items = append(y.Items, ref(askThread, "s:again"))
	if e := only(t, reconcile(t, onUnit(pr, followed), settled, []task.Task{y}, now)); !e.Open() || len(e.Items) != 1 {
		t.Fatalf("a merged own task whose thread still asks: %+v", e)
	}
}

func TestANewItemOfAMergedTasksKeyGoesToTheClosedSurvivorNotANewTask(t *testing.T) {
	y := survivor(task.DerivedID(askThread, "s:ask"), prTaskID, []unit.Unit{askThread, pr}, nil,
		[]task.ItemRef{ref(askThread, "s:ask")})
	y.State, y.Category = "done", task.CategoryDone
	cs := reconciled(t, onUnit(pr, followed), []items.Item{item("gh:new", keyed)}, me, []task.Task{y}, bundled, now)
	if len(cs) != 1 || cs[0].Op == task.OpCreate || cs[0].Task.ID != y.ID || !slices.Contains(cs[0].Task.Items, ref(pr, "gh:new")) {
		t.Fatalf("the merged task's id was derived again: %+v", cs)
	}
	if !cs[0].Task.Open() {
		t.Fatalf("a new ask of me reopens the survivor: %+v", cs[0].Task)
	}
}

func TestAMovedItemThatReopensStaysOnTheSurvivor(t *testing.T) {
	y := survivor(task.DerivedID(askThread, "s:ask"), prTaskID, []unit.Unit{askThread, pr},
		[]task.ItemRef{ref(askThread, "s:ask")}, []task.ItemRef{ref(pr, "gh:review")})
	cs := reconciled(t, onUnit(pr, followed), []items.Item{item("gh:review", keyed, func(it *items.Item) { it.UpdatedAt = now })}, me, []task.Task{y}, bundled, now)
	if len(cs) != 1 || cs[0].Task.ID != y.ID || !slices.Contains(cs[0].Task.Items, ref(pr, "gh:review")) || !cs[0].Task.Changed {
		t.Fatalf("the reopened item: %+v", cs)
	}
}

func TestANewItemOfAMovedKeyGoesToTheSurvivor(t *testing.T) {
	y := survivor(task.DerivedID(askThread, "s:ask"), prTaskID, []unit.Unit{askThread, pr},
		[]task.ItemRef{ref(askThread, "s:ask"), ref(pr, "gh:review")}, nil)
	cs := reconciled(t, onUnit(pr, followed), []items.Item{item("gh:review", keyed), item("gh:two", keyed)}, me, []task.Task{y}, bundled, now)
	if len(cs) != 1 || cs[0].Task.ID != y.ID || len(cs[0].Task.Items) != 3 {
		t.Fatalf("a sibling of a moved key: %+v", cs)
	}
	sibling := reconciled(t, onUnit(askThread, followed), []items.Item{slackItem(askThread, "s:ask"), slackItem(askThread, "s:other")}, me, []task.Task{y}, bundled, now)
	if !slices.ContainsFunc(sibling, func(c task.Change) bool {
		return c.Op == task.OpCreate && c.Task.ID == task.DerivedID(askThread, "s:other")
	}) {
		t.Fatalf("a new ask of the thread is its own task, as before the merge: %+v", sibling)
	}
}

func TestAMovedUnitsOwnTaskRulesDoNotTakeItsItemsBack(t *testing.T) {
	y := survivor(task.DerivedID(askThread, "s:ask"), prTaskID, []unit.Unit{askThread, pr},
		[]task.ItemRef{ref(askThread, "s:ask"), ref(pr, "gh:review")}, nil)
	other := task.Task{ID: "t-00000000000000o9", State: "next", Category: task.CategoryReady, Units: []unit.Unit{pr},
		Origin: task.OriginUser, Created: now.Add(-72 * time.Hour)}
	cs := reconciled(t, onUnit(pr, followed), []items.Item{item("gh:review", keyed), item("gh:two", keyed)}, me, []task.Task{other, y}, bundled, now)
	for _, c := range cs {
		if c.Task.ID == other.ID && len(c.Task.Items) > 0 {
			t.Fatalf("the unit's own task took the moved items: %+v", cs)
		}
	}
	if len(cs) != 1 || cs[0].Task.ID != y.ID {
		t.Fatalf("changes %+v", cs)
	}
}

func TestAMergedTasksTitleNoLongerFollowsItsItem(t *testing.T) {
	y := survivor(prTaskID, task.DerivedID(askThread, "s:ask"), []unit.Unit{pr, askThread}, []task.ItemRef{ref(pr, "gh:review")}, nil)
	e := only(t, reconcile(t, onUnit(pr, followed), []items.Item{item("gh:review", keyed, func(it *items.Item) { it.Summary = "reworded" })}, []task.Task{y}, now))
	if e.Title != y.Title {
		t.Fatalf("title %q", e.Title)
	}
}
