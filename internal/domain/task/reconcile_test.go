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
	mute := func(f *task.UnitFacts) { f.Mute = &task.Mute{Until: now.Add(48 * time.Hour)}; f.Cares = true }
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
		if e := only(t, reconcile(t, facts(), its, nil, now)); !e.Due().Equal(want) {
			t.Errorf("%q: deadline %v, want %v", in, e.Due(), want)
		}
	}
}

func TestMutedUnitTaskIsSnoozed(t *testing.T) {
	until := now.Add(72 * time.Hour)
	mute := func(f *task.UnitFacts) { f.Mute = &task.Mute{Until: until} }
	its := []items.Item{item("a", func(it *items.Item) { it.Initial = items.InitialNext })}
	e := only(t, reconcile(t, facts(mute), its, nil, now))
	want := task.Snooze{Until: until, Return: task.State("next"), Mute: true}
	if e.Category != task.CategoryDeferred || e.Snooze != want {
		t.Fatalf("new entry %+v", e)
	}
	plain := only(t, reconcile(t, facts(), its, nil, now))
	got := only(t, reconcile(t, facts(mute), its, []task.Task{plain}, now))
	if got.Category != task.CategoryDeferred || got.Snooze != want {
		t.Fatalf("existing entry %+v", got)
	}
}

func TestMuteUntilStateChangeWakesOnActivity(t *testing.T) {
	its := []items.Item{item("a")}
	for _, change := range []bool{true, false} {
		f := facts(func(f *task.UnitFacts) { f.Mute = &task.Mute{UntilStateChange: change} })
		e := only(t, reconcile(t, f, its, nil, now))
		f.LastHuman = now.Add(time.Hour)
		cs := activity(t, f, []task.Task{e}, bundled, now.Add(2*time.Hour))
		if !change {
			if len(cs) != 0 {
				t.Fatalf("activity moved a muted entry: %+v", cs)
			}
			continue
		}
		if len(cs) != 1 || cs[0].Task.Category != task.CategoryInbox || !cs[0].Task.Changed || cs[0].Task.Snoozed() {
			t.Fatalf("activity: %+v", cs)
		}
		if again := reconciled(t, f, its, me, []task.Task{cs[0].Task}, bundled, now.Add(3*time.Hour)); len(again) != 0 {
			t.Fatalf("the woken entry was muted again: %+v", again)
		}
	}
}

func TestUnmuteRestoresTheState(t *testing.T) {
	mute := func(f *task.UnitFacts) { f.Mute = &task.Mute{} }
	its := []items.Item{item("a", owedBy(ana))}
	e := only(t, reconcile(t, facts(mute), its, nil, now))
	if e.Category != task.CategoryDeferred {
		t.Fatalf("muted: %+v", e)
	}
	e = only(t, reconcile(t, facts(), its, []task.Task{e}, now))
	if e.Category != task.CategoryWaiting || e.Snooze != (task.Snooze{}) {
		t.Fatalf("unmuted: %+v", e)
	}
	lapsed := func(f *task.UnitFacts) { f.Mute = &task.Mute{Until: now.Add(-time.Minute)} }
	if es := reconcile(t, facts(lapsed), its, nil, now); es[0].Category != task.CategoryWaiting {
		t.Fatalf("a lapsed mute: %+v", es[0])
	}
}

func TestEmptyKeyIsNeverMe(t *testing.T) {
	blank := task.Me{Keys: []string{"", "github:me"}}
	it := item("a", func(it *items.Item) { it.WaitingOn, it.Requester = []items.Person{{Name: "someone"}}, items.Person{} })
	if cs := reconciled(t, facts(), []items.Item{it}, blank, nil, bundled, now); len(cs) != 0 {
		t.Fatalf("an unknown person counted as me: %+v", cs)
	}
}

func TestWakeReturnsToItsState(t *testing.T) {
	e := task.Task{State: "scheduled", Category: task.CategoryDeferred, Snooze: task.Snooze{Until: now, Return: task.State("next")}}
	got, ok, _ := task.Wake(e, bundled, now)
	if !ok || got.Category != task.CategoryReady || got.Snooze != (task.Snooze{}) {
		t.Fatalf("woke %+v %v", got, ok)
	}
	if _, ok, _ := task.Wake(e, bundled, now.Add(-time.Second)); ok {
		t.Fatal("woke early")
	}
	e.Snooze = task.Snooze{OnActivity: true}
	if _, ok, _ := task.Wake(e, bundled, now); ok {
		t.Fatal("woke an entry with no date")
	}
}

func TestMutedUnitActivitySparesUserSchedule(t *testing.T) {
	f := facts(func(f *task.UnitFacts) { f.Mute, f.LastHuman = &task.Mute{Until: now.Add(time.Hour)}, now })
	have := []task.Task{{ID: "e-mine", State: "scheduled", Category: task.CategoryDeferred, Snooze: task.Snooze{OnActivity: true, Return: task.State("next")}, Created: now.Add(-time.Hour)}}
	if cs := activity(t, f, have, bundled, now); len(cs) != 0 {
		t.Fatalf("activity on a muted unit moved %+v", cs)
	}
	if cs := reconciled(t, f, nil, me, have, bundled, now); len(cs) != 0 {
		t.Fatalf("the mute took over a user's schedule: %+v", cs)
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
		open.State, open.Category, open.Snooze = task.State(held.state), held.c, task.Snooze{}
		if held.c == task.CategoryDeferred {
			open.Snooze = task.Snooze{Until: now.Add(48 * time.Hour), Return: "waiting"}
		}
		resolved(&its[0])
		closed := only(t, reconcile(t, facts(), its, []task.Task{open}, now))
		closed.State, closed.Category, closed.Snooze = open.State, open.Category, open.Snooze
		its[0].State = "open"
		got := only(t, reconcile(t, facts(), its, []task.Task{closed}, now.Add(time.Hour)))
		if got.Category != held.c || !got.Changed || got.Snooze != open.Snooze || len(got.Items) != 1 {
			t.Fatalf("an item reopened on a %s task: %+v", held.state, got)
		}
		f := facts(func(f *task.UnitFacts) { f.LastHuman = now.Add(2 * time.Hour) })
		for _, c := range activity(t, f, []task.Task{open}, bundled, now.Add(3*time.Hour)) {
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
	marked := done
	marked.Items = []task.ItemRef{{Unit: pr, Item: "a"}}
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
