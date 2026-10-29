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
	brt, _ = time.LoadLocation("America/Sao_Paulo")
	now    = time.Date(2026, 10, 20, 10, 0, 0, 0, brt)
	me     = task.Me{Keys: []string{"github:me", "slack:U000ME"}}
	meP    = items.Person{Key: "github:me", Name: "Me"}
	ana    = items.Person{Key: "github:ana", Name: "Ana"}
	bob    = items.Person{Key: "github:bob", Name: "Bob"}
	pr     = unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
)

func item(id string, mod ...func(*items.Item)) items.Item {
	it := items.Item{ID: id, Unit: pr, Kind: "request", Summary: "do " + id, State: "open", Requester: ana,
		WaitingOn: []items.Person{meP}, CreatedAt: now.Add(-time.Hour)}
	for _, m := range mod {
		m(&it)
	}
	return it
}

func owedBy(p items.Person) func(*items.Item) {
	return func(it *items.Item) { it.WaitingOn = []items.Person{p}; it.Requester = meP }
}

func resolved(it *items.Item) { it.State = "resolved" }

func facts(mod ...func(*task.UnitFacts)) task.UnitFacts {
	f := task.UnitFacts{Unit: pr}
	for _, m := range mod {
		m(&f)
	}
	return f
}

// apply folds cs into have by id, as the store would.
func apply(have []task.Task, cs []task.Change) []task.Task {
	out := slices.Clone(have)
	for _, c := range cs {
		i := slices.IndexFunc(out, func(e task.Task) bool { return e.ID == c.Task.ID })
		if i < 0 {
			out = append(out, c.Task)
		} else {
			out[i] = c.Task
		}
	}
	return out
}

func reconcile(t *testing.T, f task.UnitFacts, its []items.Item, have []task.Task, at time.Time) []task.Task {
	t.Helper()
	return apply(have, reconciled(t, f, its, me, have, bundled, at))
}

func only(t *testing.T, es []task.Task) task.Task {
	t.Helper()
	if len(es) != 1 {
		t.Fatalf("%d entries, want 1: %+v", len(es), es)
	}
	return es[0]
}

func TestOpenItemOwedByMeIsAnAction(t *testing.T) {
	its := []items.Item{item("i1", func(it *items.Item) { it.Contexts = []string{"@reply"} })}
	e := only(t, reconcile(t, facts(), its, nil, now))
	if e.Category != task.CategoryInbox || !slices.Equal(e.Prop("contexts").List, []string{"@reply"}) || e.Origin != task.OriginDerived {
		t.Fatalf("entry %+v", e)
	}
	if !slices.Equal(e.Items, []task.ItemRef{{Unit: pr, Item: "i1"}}) || !slices.Equal(e.Units, []unit.Unit{pr}) {
		t.Fatalf("refs %+v units %+v", e.Items, e.Units)
	}
	e.State, e.Category = "next", task.CategoryReady
	e.SetProp("contexts", task.Value{List: []string{"@decide"}}, task.WriterPerson, now)
	its[0].Summary = "do it better"
	got := only(t, reconcile(t, facts(), its, []task.Task{e}, now.Add(time.Hour)))
	if got.Category != task.CategoryReady || got.Title != "do it better" || !slices.Equal(got.Prop("contexts").List, []string{"@decide"}) {
		t.Fatalf("after the item changed: %+v", got)
	}
}

func mapsEqual[K comparable, V comparable](a, b map[K]V) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

func TestOthersItemsIgnoredWithoutCare(t *testing.T) {
	others := func(it *items.Item) { it.WaitingOn, it.Requester = []items.Person{ana}, bob }
	its := []items.Item{item("a", others)}
	if es := reconcile(t, facts(), its, nil, now); len(es) != 0 {
		t.Fatalf("entries %+v", es)
	}
	cares := func(f *task.UnitFacts) { f.Cares = true }
	if e := only(t, reconcile(t, facts(cares), its, nil, now)); e.Category != task.CategoryWaiting {
		t.Fatalf("with care: %+v", e)
	}
}

func TestOnlyItemsCreateTasks(t *testing.T) {
	f := facts(func(f *task.UnitFacts) { f.StartedByMe, f.Cares, f.LastHuman = true, true, now })
	if es := reconcile(t, f, nil, nil, now); len(es) != 0 {
		t.Fatalf("entries %+v", es)
	}
}

func TestReconcileIsIdempotent(t *testing.T) {
	mute := func(f *task.UnitFacts) { f.Cares = true }
	its := []items.Item{item("a"), item("b", owedBy(ana)), item("c", owedBy(bob), func(it *items.Item) { it.Deadline = "2026-10-30" })}
	first := reconcile(t, facts(mute), its, nil, now)
	cs := reconciled(t, facts(mute), its, me, first, bundled, now.Add(time.Minute))
	if len(cs) != 0 {
		t.Fatalf("second run changes %+v", cs)
	}
	again := reconcile(t, facts(mute), its, nil, now)
	for i := range first {
		if first[i].ID != again[i].ID {
			t.Fatalf("ids differ: %s %s", first[i].ID, again[i].ID)
		}
	}
}

func TestItemDeadlineParses(t *testing.T) {
	cases := map[string]time.Time{
		"2026-10-30T12:00:00Z": time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC),
		"2026-10-30":           time.Date(2026, 10, 30, 18, 0, 0, 0, brt),
		"next week":            {},
	}
	for in, want := range cases {
		its := []items.Item{item("a", func(it *items.Item) { it.Deadline = in })}
		if e := only(t, reconcile(t, facts(), its, nil, now)); !e.Prop("due").Time.Equal(want) {
			t.Errorf("%q: deadline %v, want %v", in, e.Prop("due").Time, want)
		}
	}
}

func TestEmptyKeyIsNeverMe(t *testing.T) {
	blank := task.Me{Keys: []string{"", "github:me"}}
	it := item("a", func(it *items.Item) { it.WaitingOn, it.Requester = []items.Person{{Name: "someone"}}, items.Person{} })
	if cs := reconciled(t, facts(), []items.Item{it}, blank, nil, bundled, now); len(cs) != 0 {
		t.Fatalf("an unknown person counted as me: %+v", cs)
	}
}

func TestReconcileMakesOneTaskPerDelegatedItem(t *testing.T) {
	its := []items.Item{item("a", owedBy(ana)), item("b", owedBy(ana)), item("c", owedBy(bob)), item("d")}
	es := reconcile(t, facts(), its, nil, now)
	if len(es) != 4 {
		t.Fatalf("%d tasks for four items, want one each: %+v", len(es), es)
	}
	for _, e := range es {
		want := task.CategoryWaiting
		if e.Items[0].Item == "d" {
			want = task.CategoryInbox
		}
		if len(e.Items) != 1 || e.ID != task.DerivedID(pr, e.Items[0].Item) || e.Category != want || e.Title != "do "+e.Items[0].Item {
			t.Errorf("task %+v, want one item in %s", e, want)
		}
	}
	resolved(&its[0])
	es = reconcile(t, facts(), its, es, now.Add(time.Hour))
	for _, e := range es {
		want := task.CategoryWaiting
		switch e.ID {
		case task.DerivedID(pr, "a"):
			want = task.CategoryDone
		case task.DerivedID(pr, "d"):
			want = task.CategoryInbox
		}
		if e.Category != want {
			t.Errorf("after a resolved: %s in %s, want %s", e.ID, e.Category, want)
		}
	}
	merged := es[slices.IndexFunc(es, func(e task.Task) bool { return e.ID == task.DerivedID(pr, "b") })]
	merged.Items = append(slices.Clone(merged.Items), task.ItemRef{Unit: pr, Item: "c"})
	rest := slices.DeleteFunc(slices.Clone(es), func(e task.Task) bool { return e.ID == task.DerivedID(pr, "c") })
	rest[slices.IndexFunc(rest, func(e task.Task) bool { return e.ID == merged.ID })] = merged
	if cs := reconciled(t, facts(), its, me, rest, bundled, now.Add(2*time.Hour)); len(cs) != 0 {
		t.Fatalf("a task holding two items split them again: %+v", cs)
	}
}

func TestReconcileMakesNoCloseTasks(t *testing.T) {
	mine := func(f *task.UnitFacts) { f.StartedByMe = true }
	its := []items.Item{item("a", resolved), item("b", owedBy(ana), resolved)}
	if cs := reconciled(t, facts(mine), its, me, nil, bundled, now); len(cs) != 0 {
		t.Fatalf("a unit I started, its items resolved, made %+v", cs)
	}
	open := []items.Item{item("a", owedBy(ana))}
	e := only(t, reconcile(t, facts(mine), open, nil, now))
	resolved(&open[0])
	got := only(t, reconcile(t, facts(mine), open, []task.Task{e}, now))
	if got.ID != e.ID || got.Category != task.CategoryDone {
		t.Fatalf("the resolved item's task: %+v", got)
	}
}

func TestAReopenedItemMarksChangedAndStaysOutOfTheInbox(t *testing.T) {
	its := []items.Item{item("a", owedBy(ana))}
	e := only(t, reconcile(t, facts(), its, nil, now))
	for _, held := range []struct {
		state string
		c     task.Category
	}{{"waiting", task.CategoryWaiting}, {"scheduled", task.CategoryDeferred}, {"next", task.CategoryReady}} {
		open := e
		open.State, open.Category, open.Asleep = task.State(held.state), held.c, held.c == task.CategoryDeferred
		resolved(&its[0])
		closed := only(t, reconcile(t, facts(), its, []task.Task{open}, now))
		closed.State, closed.Category, closed.Asleep = open.State, open.Category, open.Asleep
		its[0].State = "open"
		got := only(t, reconcile(t, facts(), its, []task.Task{closed}, now.Add(time.Hour)))
		if got.Category != held.c || !got.Changed || got.Asleep != open.Asleep || len(got.Items) != 1 {
			t.Fatalf("an item reopened on a %s task: %+v", held.state, got)
		}
		f := facts(func(f *task.UnitFacts) { f.LastHuman = now.Add(2 * time.Hour) })
		for _, c := range activity(f, []task.Task{open}, now.Add(3*time.Hour)) {
			if c.Task.Category == task.CategoryInbox || !c.Task.Changed {
				t.Fatalf("a new message on a %s task: %+v", held.state, c.Task)
			}
		}
	}
	resolved(&its[0])
	done := only(t, reconcile(t, facts(), its, []task.Task{e}, now))
	its[0].State = "open"
	got := only(t, reconcile(t, facts(), its, []task.Task{done}, now.Add(time.Hour)))
	if got.Category != task.CategoryReady || !got.Changed {
		t.Fatalf("an item reopened on a done task: %+v", got)
	}
	dropped := done
	dropped.State, dropped.Category = "trash", task.CategoryDropped
	if got := only(t, reconcile(t, facts(), its, []task.Task{dropped}, now.Add(time.Hour))); got.Category != task.CategoryDropped {
		t.Fatalf("an item reopened on a dropped task: %+v", got)
	}
	two := []items.Item{item("a", owedBy(ana)), item("b", owedBy(ana))}
	both := only(t, reconcile(t, facts(), two[:1], nil, now))
	both.Items = append(both.Items, task.ItemRef{Unit: pr, Item: "b"})
	resolved(&two[0])
	held := only(t, reconcile(t, facts(), two, []task.Task{both}, now))
	if held.Category != task.CategoryWaiting || !slices.Equal(held.Items, []task.ItemRef{{Unit: pr, Item: "b"}}) {
		t.Fatalf("a two-item task, one item resolved: %+v", held)
	}
	two[0].State = "open"
	got = only(t, reconcile(t, facts(), two, []task.Task{held}, now.Add(time.Hour)))
	if got.Category != task.CategoryWaiting || !got.Changed || len(got.Items) != 2 {
		t.Fatalf("an item reopened on a two-item task: %+v", got)
	}
	merged := []items.Item{item("a", owedBy(ana)), item("b", owedBy(ana))}
	holder := only(t, reconcile(t, facts(), merged[:1], nil, now))
	holder.Items = append(holder.Items, task.ItemRef{Unit: pr, Item: "b"})
	tombstone := task.Task{ID: task.DerivedID(pr, "b"), State: "trash", Category: task.CategoryDropped, Units: []unit.Unit{pr}, Origin: task.OriginDerived}
	resolved(&merged[1])
	afterResolve := reconcile(t, facts(), merged, []task.Task{holder, tombstone}, now)
	merged[1].State = "open"
	cs := reconciled(t, facts(), merged, me, afterResolve, bundled, now.Add(time.Hour))
	if len(cs) != 1 || cs[0].Task.ID != holder.ID || !cs[0].Task.Changed || cs[0].Task.Category != task.CategoryWaiting || len(cs[0].Task.Items) != 2 {
		t.Fatalf("an item merged into a task, resolved then reopened: %+v", cs)
	}
	marked := done
	marked.Items, marked.Resolved = []task.ItemRef{{Unit: pr, Item: "a"}}, nil
	if cs := reconciled(t, facts(), its, me, []task.Task{marked}, bundled, now); len(cs) != 0 {
		t.Fatalf("a task the user finished while its item is open came back: %+v", cs)
	}
}

func TestAnItemProposesACategory(t *testing.T) {
	its := []items.Item{
		item("ready", func(it *items.Item) { it.Initial = items.InitialNext }),
		item("inbox", func(it *items.Item) { it.Initial = items.InitialInbox }),
		item("none"),
		item("theirs", owedBy(ana), func(it *items.Item) { it.Initial = items.InitialNext }),
	}
	got := map[string]task.State{}
	for _, e := range reconcile(t, facts(func(f *task.UnitFacts) { f.Kind = "pull-request" }), its, nil, now) {
		got[e.Items[0].Item] = e.State
		if k := e.Prop(task.PropKind); k.Text != "pull-request" || e.Written[task.PropKind].By != task.WriterPlugin {
			t.Errorf("%s: kind %+v", e.ID, k)
		}
	}
	want := map[string]task.State{"ready": "next", "inbox": "inbox", "none": "inbox", "theirs": "next"}
	if !mapsEqual(got, want) {
		t.Fatalf("states %v, want %v", got, want)
	}
	if _, err := task.Reconcile(facts(), []items.Item{item("x", func(it *items.Item) { it.Initial = "next" })}, me, nil, bundled, now); err == nil {
		t.Fatal("a state name as the proposed category was taken")
	}
}

func keyed(it *items.Item) { it.TaskKey = pr.String() }

func TestItemsOfOneTaskKeyAreOneTask(t *testing.T) {
	cared := facts(func(f *task.UnitFacts) { f.Cares, f.Carded = true, true })
	es := reconcile(t, cared, []items.Item{item("gh:review", keyed), item("gh:fix", keyed, owedBy(ana))}, nil, now)
	e := only(t, es)
	if e.ID != task.DerivedID(pr, pr.String()) || len(e.Items) != 2 || e.Units[0] != pr {
		t.Fatalf("one task per key: %+v", e)
	}
	es = reconcile(t, cared, []items.Item{item("gh:review", keyed, resolved), item("gh:fix", keyed, resolved), item("gh:merge", keyed)}, es, now)
	if e := only(t, es); e.ID != task.DerivedID(pr, pr.String()) || !e.Open() || len(e.Items) != 1 {
		t.Fatalf("a new situation joins the task holding the key: %+v", e)
	}
	es = reconcile(t, cared, []items.Item{item("gh:merge", keyed, resolved)}, es, now)
	if e := only(t, es); !e.Open() || len(e.Items) != 0 {
		t.Fatalf("a unit's task outlives its asks: %+v", e)
	}
	es = reconcile(t, facts(func(f *task.UnitFacts) { f.Carded = true }), []items.Item{item("gh:merge", keyed, resolved)}, es, now)
	if e := only(t, es); e.Category != task.CategoryDropped {
		t.Fatalf("a unit no longer followed drops its task: %+v", e)
	}
}

func TestAKeyNoTaskHoldsGoesToTheUnitsOwnTask(t *testing.T) {
	own := task.Task{ID: "t-00000000000000a1", State: "next", Category: task.CategoryReady, Title: "Follow acme/api#7", Units: []unit.Unit{pr},
		Origin: task.OriginDerived, Created: now.Add(-time.Hour)}
	es := reconcile(t, facts(func(f *task.UnitFacts) { f.Cares, f.Carded = true, true }), []items.Item{item("gh:review", keyed)}, []task.Task{own}, now)
	if e := only(t, es); e.ID != own.ID || len(e.Items) != 1 {
		t.Fatalf("the unit's own task takes its key: %+v", es)
	}
	if es := reconcile(t, facts(), nil, []task.Task{own}, now); only(t, es).Category != task.CategoryReady {
		t.Fatalf("a task with no item is never resolved for lack of items: %+v", es)
	}
}

func TestAnOpenPRsDoneTaskTakesItsItemsBack(t *testing.T) {
	done := task.Task{ID: "t-00000000000000a2", State: "done", Category: task.CategoryDone, Title: "Review acme/api#7", Units: []unit.Unit{pr},
		Origin: task.OriginDerived, Created: now.Add(-48 * time.Hour), Updated: now.Add(-24 * time.Hour)}
	es := reconcile(t, facts(func(f *task.UnitFacts) { f.Cares, f.Carded = true, true }), []items.Item{item("gh:follow", keyed, owedBy(ana))}, []task.Task{done}, now)
	if e := only(t, es); e.ID != done.ID || e.Category != task.CategoryReady || !e.Changed || len(e.Items) != 1 {
		t.Fatalf("an open pull request whose task is done: %+v", es)
	}
}

func TestFollowingAUnitAgainReopensTheTaskReconcileDropped(t *testing.T) {
	cared := facts(func(f *task.UnitFacts) { f.Cares, f.Carded = true, true })
	es := reconcile(t, cared, []items.Item{item("gh:follow", keyed, owedBy(ana))}, nil, now)
	es = reconcile(t, facts(func(f *task.UnitFacts) { f.Carded = true }), []items.Item{item("gh:follow", keyed, owedBy(ana), resolved)}, es, now)
	if e := only(t, es); e.Category != task.CategoryDropped {
		t.Fatalf("untracked: %+v", e)
	}
	es = reconcile(t, cared, []items.Item{item("gh:follow", keyed, owedBy(ana))}, es, now)
	if e := only(t, es); e.Category != task.CategoryReady || !e.Changed || !e.Prop(task.PropUnfollowed).Time.IsZero() {
		t.Fatalf("followed again: %+v", e)
	}
	dropped := only(t, es)
	dropped.State, dropped.Category = "trash", task.CategoryDropped
	if e := only(t, reconcile(t, cared, []items.Item{item("gh:follow", keyed, owedBy(ana))}, []task.Task{dropped}, now)); e.Category != task.CategoryDropped {
		t.Fatalf("a task the person dropped came back: %+v", e)
	}
}

func TestAQuietUnitKeepsItsTaskOwedByNobody(t *testing.T) {
	quiet := item("gh:follow", keyed, func(it *items.Item) { it.WaitingOn, it.Requester = nil, meP })
	e := only(t, reconcile(t, facts(func(f *task.UnitFacts) { f.Cares, f.Carded = true, true }), []items.Item{quiet}, nil, now))
	if e.Category != task.CategoryWaiting || len(e.Items) != 1 {
		t.Fatalf("a quiet followed unit's task %+v", e)
	}
}

func TestAKeyGoesToTheOpenTaskThatLinksItsUnit(t *testing.T) {
	thread := unit.Unit{Provider: "slack", Key: "T1/C1/1.1"}
	start := task.Task{ID: "t-00000000000000a3", State: "next", Category: task.CategoryReady, Title: "Ship the export",
		Units: []unit.Unit{thread, pr}, Origin: task.OriginUser, Created: now.Add(-time.Hour)}
	es := reconcile(t, facts(func(f *task.UnitFacts) { f.Cares, f.Carded = true, true }), []items.Item{item("gh:follow", keyed, owedBy(ana))}, []task.Task{start}, now)
	if e := only(t, es); e.ID != start.ID || len(e.Items) != 1 {
		t.Fatalf("a task holding the pull request as a link: %+v", es)
	}
}
