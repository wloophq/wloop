package merge_test

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/merge"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var (
	t0     = time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	now    = t0.Add(48 * time.Hour)
	later  = now.Add(24 * time.Hour)
	pr     = unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	thread = unit.Unit{Provider: "slack", Key: "T1/C1/1.1"}
	dm     = unit.Unit{Provider: "slack", Key: "T1/D9/2.2"}
)

type profile struct{}

func (profile) CategoryOf(s string) (task.Category, bool) {
	switch s {
	case "inbox":
		return task.CategoryInbox, true
	case "next":
		return task.CategoryReady, true
	case "done":
		return task.CategoryDone, true
	}
	return "", false
}

func (profile) FirstOf(c task.Category) string {
	return map[task.Category]string{task.CategoryInbox: "inbox", task.CategoryReady: "next", task.CategoryDone: "done"}[c]
}

func (profile) PropOf(r task.Role) (string, []string) {
	return map[task.Role]string{task.RoleMerged: "merged", task.RoleRelated: "related", task.RoleArea: "area"}[r], nil
}

var st = profile{}

func ref(u unit.Unit, item string) task.ItemRef { return task.ItemRef{Unit: u, Item: item} }

// prTask is the task of a pull request, X in most tests.
func prTask() task.Task {
	e := task.Task{ID: "t-00000000000000a1", State: "next", Category: task.CategoryReady, Title: "Review acme/api#7",
		Notes: "the pull request", Items: []task.ItemRef{ref(pr, "gh:review")}, Resolved: []task.ItemRef{ref(pr, "gh:ci")},
		Units: []unit.Unit{pr}, Origin: task.OriginDerived, Created: t0.Add(time.Hour), Updated: t0.Add(2 * time.Hour),
		OpenChanged: t0.Add(time.Hour), Parent: "t-00000000000000p1", Position: 2}
	e.SetProp("area", task.Value{Text: "api"}, task.WriterPerson, t0.Add(2*time.Hour))
	return e
}

// askTask is a Slack ask about the same work, Y in most tests.
func askTask() task.Task {
	e := task.Task{ID: "t-00000000000000b2", State: "inbox", Category: task.CategoryInbox, Title: "Finish acme/api#7",
		Items: []task.ItemRef{ref(thread, "s:ask")}, Units: []unit.Unit{thread}, Origin: task.OriginDerived, Created: t0,
		Updated: t0.Add(3 * time.Hour), OpenChanged: t0}
	e.SetProp("area", task.Value{Text: "web"}, task.WriterPerson, t0.Add(3*time.Hour))
	return e
}

func fold(t *testing.T, from, into task.Task) (task.Task, merge.Record) {
	t.Helper()
	y, r, err := merge.Fold(from, into, st, now)
	if err != nil {
		t.Fatal(err)
	}
	return y, r
}

func openProject(children ...task.Task) merge.Project {
	return merge.Project{Task: task.Task{ID: "t-00000000000000p1", State: "next", Category: task.CategoryReady}, Children: children}
}

func TestUnfoldGivesBackTheMergedTaskByUnit(t *testing.T) {
	x, y := prTask(), askTask()
	merged, r := fold(t, x, y)
	merged.Items = append(merged.Items, ref(pr, "gh:new"))
	from, rest, err := merge.Unfold(merged, r, openProject(), st, later)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(from.Items, ref(pr, "gh:new")) || slices.ContainsFunc(rest.Items, func(r task.ItemRef) bool { return r.Unit == pr }) {
		t.Fatalf("every ref of the pull request goes back to it: from %v, rest %v", from.Items, rest.Items)
	}
	if !slices.Equal(from.Units, x.Units) || !slices.Equal(rest.Units, y.Units) {
		t.Fatalf("units: from %v, rest %v", from.Units, rest.Units)
	}
}

func TestUnfoldGivesBackAnUntouchedTaskFieldByField(t *testing.T) {
	x, y := prTask(), askTask()
	merged, r := fold(t, x, y)
	from, rest, err := merge.Unfold(merged, r, openProject(), st, later)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(from, x) {
		t.Fatalf("from\n got %+v\nwant %+v", from, x)
	}
	rest.Updated = y.Updated
	if !rest.Same(y) || rest.Prop("merged").List != nil {
		t.Fatalf("rest\n got %+v\nwant %+v", rest, y)
	}
}

func TestUnfoldKeepsWhatArrivedOnTheSurvivorsOwnUnits(t *testing.T) {
	merged, r := fold(t, prTask(), askTask())
	merged.Items = append(merged.Items, ref(thread, "s:followup"))
	_, rest, err := merge.Unfold(merged, r, openProject(), st, later)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(rest.Items, []task.ItemRef{ref(thread, "s:ask"), ref(thread, "s:followup")}) {
		t.Fatalf("rest items %v", rest.Items)
	}
}

func TestUnfoldGivesBackWhatArrivedOnTheMergedTasksUnits(t *testing.T) {
	merged, r := fold(t, prTask(), askTask())
	merged.Resolved = append(merged.Resolved, ref(pr, "gh:lint"))
	from, rest, err := merge.Unfold(merged, r, openProject(), st, later)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(from.Resolved, ref(pr, "gh:lint")) || slices.Contains(rest.Resolved, ref(pr, "gh:lint")) {
		t.Fatalf("from %v, rest %v", from.Resolved, rest.Resolved)
	}
	if !from.Updated.Equal(later) {
		t.Fatalf("a task given back changed takes the split's time: %v", from.Updated)
	}
}

func TestUnfoldGivesBackARefResolvedWhileMergedAsResolved(t *testing.T) {
	merged, r := fold(t, prTask(), askTask())
	merged.Items = slices.DeleteFunc(merged.Items, func(o task.ItemRef) bool { return o == ref(pr, "gh:review") })
	merged.Resolved = append(merged.Resolved, ref(pr, "gh:review"))
	from, _, err := merge.Unfold(merged, r, openProject(), st, later)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(from.Items, ref(pr, "gh:review")) || !slices.Contains(from.Resolved, ref(pr, "gh:review")) {
		t.Fatalf("items %v, resolved %v", from.Items, from.Resolved)
	}
}

func TestUnfoldClearsTheParentOfAClosedProjectAndAppendsATakenPosition(t *testing.T) {
	merged, r := fold(t, prTask(), askTask())
	closed := openProject()
	closed.Task.Category, closed.Task.State = task.CategoryDone, "done"
	from, _, err := merge.Unfold(merged, r, closed, st, later)
	if err != nil {
		t.Fatal(err)
	}
	if from.Parent != "" || from.Position != 0 {
		t.Fatalf("a closed project's child: parent %q at %d", from.Parent, from.Position)
	}
	if from, _, err = merge.Unfold(merged, r, merge.Project{}, st, later); err != nil || from.Parent != "" {
		t.Fatalf("a project no longer stored: parent %q, %v", from.Parent, err)
	}
	taken := openProject(task.Task{ID: "t-00000000000000c3", Position: 2}, task.Task{ID: "t-00000000000000c4", Position: 3})
	if from, _, err = merge.Unfold(merged, r, taken, st, later); err != nil || from.Parent != "t-00000000000000p1" || from.Position != 4 {
		t.Fatalf("a taken position: parent %q at %d, %v", from.Parent, from.Position, err)
	}
	free := openProject(task.Task{ID: "t-00000000000000c3", Position: 1})
	if from, _, err = merge.Unfold(merged, r, free, st, later); err != nil || from.Position != 2 {
		t.Fatalf("a free position: at %d, %v", from.Position, err)
	}
}

func TestFoldMovesAnItemTheSurvivorHeldResolvedIntoItsOpenItems(t *testing.T) {
	x, y := prTask(), askTask()
	y.Resolved = []task.ItemRef{ref(pr, "gh:review")}
	merged, r := fold(t, x, y)
	if slices.Contains(merged.Resolved, ref(pr, "gh:review")) || !slices.Contains(merged.Items, ref(pr, "gh:review")) {
		t.Fatalf("items %v, resolved %v", merged.Items, merged.Resolved)
	}
	if !slices.Equal(r.Opened, []task.ItemRef{ref(pr, "gh:review")}) || slices.Contains(r.Items, ref(pr, "gh:review")) {
		t.Fatalf("record %+v", r)
	}
	_, rest, err := merge.Unfold(merged, r, openProject(), st, later)
	if err != nil || !slices.Equal(rest.Resolved, []task.ItemRef{ref(pr, "gh:review")}) || len(rest.Items) != 1 {
		t.Fatalf("split gives the survivor its resolved ref back: %+v, %v", rest, err)
	}
}

func TestFoldKeepsTheSurvivorsStatePropertiesParentAndTitle(t *testing.T) {
	x, y := prTask(), askTask()
	merged, r := fold(t, x, y)
	if merged.State != y.State || merged.Title != y.Title || merged.Parent != "" || merged.Prop("area").Text != "web" {
		t.Fatalf("survivor %+v", merged)
	}
	if !slices.Equal(merged.Prop("merged").List, []string{string(x.ID)}) || merged.Written["merged"].By != task.WriterWorkflow {
		t.Fatalf("merged property %v by %v", merged.Prop("merged"), merged.Written["merged"])
	}
	if !slices.Equal(merged.Units, []unit.Unit{thread, pr}) || !slices.Equal(r.Units, []unit.Unit{pr}) {
		t.Fatalf("units %v, record %v", merged.Units, r.Units)
	}
	if !slices.Equal(r.Items, []task.ItemRef{ref(pr, "gh:review")}) || !slices.Equal(r.Resolved, []task.ItemRef{ref(pr, "gh:ci")}) {
		t.Fatalf("record %+v", r)
	}
	if r.From != x.ID || r.Into != y.ID || !r.At.Equal(now) || !reflect.DeepEqual(r.Task, x) {
		t.Fatalf("record %+v", r)
	}
	if _, _, err := merge.Fold(x, x, st, now); err == nil {
		t.Fatal("a task merged into itself")
	}
}

func TestFoldListsTheTasksMergedIntoTheMergedOne(t *testing.T) {
	w := task.Task{ID: "t-00000000000000d5", Units: []unit.Unit{dm}, Items: []task.ItemRef{ref(dm, "s:dm")}, Created: t0}
	x, y := prTask(), askTask()
	x, rw := fold(t, w, x)
	z, rx := fold(t, x, y)
	if !slices.Equal(z.Prop("merged").List, []string{string(x.ID), string(w.ID)}) {
		t.Fatalf("merged %v", z.Prop("merged").List)
	}
	back, rest, err := merge.Unfold(z, rx, openProject(), st, later)
	if err != nil || rest.Prop("merged").List != nil || !slices.Equal(back.Prop("merged").List, []string{string(w.ID)}) {
		t.Fatalf("rest %v, back %v, %v", rest.Prop("merged"), back.Prop("merged"), err)
	}
	if _, _, err := merge.Unfold(z, rw, openProject(), st, later); err == nil {
		t.Fatal("a task merged into another split from the outermost")
	}
}

func TestFoldCarriesRelationsWithoutEitherTaskAndUnfoldTakesThemOff(t *testing.T) {
	x, y := prTask(), askTask()
	x.SetProp("related", task.Value{List: []string{string(y.ID), "t-00000000000000e6"}}, task.WriterWorkflow, t0)
	y.SetProp("related", task.Value{List: []string{string(x.ID), "t-00000000000000f7"}}, task.WriterWorkflow, t0)
	merged, r := fold(t, x, y)
	if !slices.Equal(merged.Prop("related").List, []string{"t-00000000000000f7", "t-00000000000000e6"}) ||
		!slices.Equal(r.Related, []task.ID{"t-00000000000000e6"}) {
		t.Fatalf("related %v, record %v", merged.Prop("related").List, r.Related)
	}
	_, rest, err := merge.Unfold(merged, r, openProject(), st, later)
	if err != nil || !slices.Equal(rest.Prop("related").List, []string{"t-00000000000000f7", string(x.ID)}) {
		t.Fatalf("related %v, %v", rest.Prop("related").List, err)
	}
}

func TestSurvivorKeepsTheOneThePredicateHoldsOnAloneWhateverTheOrder(t *testing.T) {
	older, newer := askTask(), prTask()
	for _, c := range []struct {
		a, b         task.Task
		aHold, bHold bool
	}{{older, newer, false, true}, {newer, older, true, false}} {
		if keep, fold := merge.Survivor(c.a, c.b, c.aHold, c.bHold); keep.ID != newer.ID || fold.ID != older.ID {
			t.Fatalf("kept %s, folded %s", keep.ID, fold.ID)
		}
	}
}

func TestSurvivorKeepsTheOlderWhenThePredicateHoldsOnBothOrNeither(t *testing.T) {
	older, newer := askTask(), prTask()
	for _, holds := range []bool{true, false} {
		for _, pair := range [][2]task.Task{{older, newer}, {newer, older}} {
			if keep, _ := merge.Survivor(pair[0], pair[1], holds, holds); keep.ID != older.ID {
				t.Fatalf("holds %v: kept %s", holds, keep.ID)
			}
		}
	}
	twin := older
	twin.ID = "t-00000000000000a0"
	if keep, _ := merge.Survivor(older, twin, false, false); keep.ID != twin.ID {
		t.Fatalf("same age: kept %s, want the lower id", keep.ID)
	}
}
