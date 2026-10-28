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
	return apply(have, task.Reconcile(f, its, me, have, at))
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
	if e.Kind != task.KindAction || e.State != task.Inbox || !slices.Equal(e.Contexts, []string{"@reply"}) || e.Origin != task.OriginDerived {
		t.Fatalf("entry %+v", e)
	}
	if !slices.Equal(e.Items, []task.ItemRef{{Unit: pr, Item: "i1"}}) || !slices.Equal(e.Units, []unit.Unit{pr}) {
		t.Fatalf("refs %+v units %+v", e.Items, e.Units)
	}
	e.State, e.Contexts = task.Next, []string{"@decide"}
	its[0].Summary = "do it better"
	got := only(t, reconcile(t, facts(), its, []task.Task{e}, now.Add(time.Hour)))
	if got.State != task.Next || got.Title != "do it better" || !slices.Equal(got.Contexts, []string{"@decide"}) {
		t.Fatalf("after the item changed: %+v", got)
	}
}

func TestDerivedItemLandsWhereThePluginSays(t *testing.T) {
	its := []items.Item{
		item("next", func(it *items.Item) { it.Initial = "next" }),
		item("inbox", func(it *items.Item) { it.Initial = "inbox" }),
		item("none"),
	}
	got := map[string]task.State{}
	for _, e := range reconcile(t, facts(), its, nil, now) {
		got[e.Items[0].Item] = e.State
	}
	if want := map[string]task.State{"next": task.Next, "inbox": task.Inbox, "none": task.Inbox}; !mapsEqual(got, want) {
		t.Fatalf("states %v, want %v", got, want)
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

func TestItemsToOnePersonCollapse(t *testing.T) {
	its := []items.Item{item("a", owedBy(ana)), item("b", owedBy(ana)), item("c", owedBy(ana))}
	e := only(t, reconcile(t, facts(), its, nil, now))
	if e.Kind != task.KindWaiting || e.State != task.Waiting || len(e.Items) != 3 || e.Waiting == nil || e.Waiting.On[0].Key != ana.Key {
		t.Fatalf("entry %+v", e)
	}
	its = append(its, item("d", owedBy(bob)))
	es := reconcile(t, facts(), its, []task.Task{e}, now)
	if len(es) != 2 || es[1].Waiting.On[0].Key != bob.Key || len(es[1].Items) != 1 {
		t.Fatalf("entries %+v", es)
	}
}

func TestOthersItemsIgnoredWithoutCare(t *testing.T) {
	others := func(it *items.Item) { it.WaitingOn, it.Requester = []items.Person{ana}, bob }
	its := []items.Item{item("a", others)}
	if es := reconcile(t, facts(), its, nil, now); len(es) != 0 {
		t.Fatalf("entries %+v", es)
	}
	cares := func(f *task.UnitFacts) { f.Cares = true }
	if e := only(t, reconcile(t, facts(cares), its, nil, now)); e.Kind != task.KindWaiting {
		t.Fatalf("with care: %+v", e)
	}
}

func TestCloseActionForMyResolvedUnit(t *testing.T) {
	mine := func(f *task.UnitFacts) { f.StartedByMe = true }
	its := []items.Item{item("a", resolved), item("b", owedBy(ana), resolved)}
	e := only(t, reconcile(t, facts(mine), its, nil, now))
	if e.Kind != task.KindClose || e.State != task.Inbox {
		t.Fatalf("entry %+v", e)
	}
	posted := func(f *task.UnitFacts) { f.ClosurePosted = true }
	if got := only(t, reconcile(t, facts(mine, posted), its, []task.Task{e}, now)); got.State != task.Done {
		t.Fatalf("after the closure: %+v", got)
	}
	if es := reconcile(t, facts(mine), its[:1:1], nil, now); len(es) != 1 {
		t.Fatalf("one resolved item: %+v", es)
	}
	if es := reconcile(t, facts(mine), append(its, item("c")), nil, now); len(es) != 1 || es[0].Kind != task.KindAction {
		t.Fatalf("an open item left: %+v", es)
	}
}

func TestResolvedItemsResolveTheTask(t *testing.T) {
	its := []items.Item{item("a", owedBy(ana)), item("b", owedBy(ana))}
	e := only(t, reconcile(t, facts(), its, nil, now))
	resolved(&its[0])
	e = only(t, reconcile(t, facts(), its, []task.Task{e}, now))
	if e.State != task.Waiting || !slices.Equal(e.Items, []task.ItemRef{{Unit: pr, Item: "b"}}) {
		t.Fatalf("one resolved: %+v", e)
	}
	resolved(&its[1])
	e = only(t, reconcile(t, facts(), its, []task.Task{e}, now))
	if e.State != task.Done || len(e.Items) != 0 {
		t.Fatalf("all resolved: %+v", e)
	}
}

func TestOnlyItemsCreateTasks(t *testing.T) {
	f := facts(func(f *task.UnitFacts) { f.StartedByMe, f.Cares, f.LastHuman = true, true, now })
	if es := reconcile(t, f, nil, nil, now); len(es) != 0 {
		t.Fatalf("entries %+v", es)
	}
}

func TestDismissedStaysDismissedUntilReopen(t *testing.T) {
	its := []items.Item{item("a", owedBy(ana)), item("b", owedBy(ana))}
	e := only(t, reconcile(t, facts(), its, nil, now))
	e.State = task.Trash
	if cs := task.Reconcile(facts(), its, me, []task.Task{e}, now); len(cs) != 0 {
		t.Fatalf("changes %+v", cs)
	}
	resolved(&its[0])
	e = only(t, reconcile(t, facts(), its, []task.Task{e}, now))
	if e.State != task.Trash {
		t.Fatalf("after a resolve: %+v", e)
	}
	its[0].State = "open"
	e = only(t, reconcile(t, facts(), its, []task.Task{e}, now))
	if e.State != task.Inbox || len(e.Items) != 2 {
		t.Fatalf("after the reopen: %+v", e)
	}
}

func TestReconcileIsIdempotent(t *testing.T) {
	mute := func(f *task.UnitFacts) { f.Mute = &task.Mute{Until: now.Add(48 * time.Hour)}; f.Cares = true }
	its := []items.Item{item("a"), item("b", owedBy(ana)), item("c", owedBy(bob), func(it *items.Item) { it.Deadline = "2026-10-30" })}
	first := reconcile(t, facts(mute), its, nil, now)
	cs := task.Reconcile(facts(mute), its, me, first, now.Add(time.Minute))
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
		if e := only(t, reconcile(t, facts(), its, nil, now)); !e.Deadline.Equal(want) {
			t.Errorf("%q: deadline %v, want %v", in, e.Deadline, want)
		}
	}
}

func TestMutedUnitTaskIsSnoozed(t *testing.T) {
	until := now.Add(72 * time.Hour)
	mute := func(f *task.UnitFacts) { f.Mute = &task.Mute{Until: until} }
	its := []items.Item{item("a", func(it *items.Item) { it.Initial = "next" })}
	e := only(t, reconcile(t, facts(mute), its, nil, now))
	want := task.Snooze{Until: until, Return: task.Next, Mute: true}
	if e.State != task.Scheduled || e.Snooze != want {
		t.Fatalf("new entry %+v", e)
	}
	plain := only(t, reconcile(t, facts(), its, nil, now))
	got := only(t, reconcile(t, facts(mute), its, []task.Task{plain}, now))
	if got.State != task.Scheduled || got.Snooze != want {
		t.Fatalf("existing entry %+v", got)
	}
}

func TestMuteUntilStateChangeWakesOnActivity(t *testing.T) {
	its := []items.Item{item("a")}
	for _, change := range []bool{true, false} {
		f := facts(func(f *task.UnitFacts) { f.Mute = &task.Mute{UntilStateChange: change} })
		e := only(t, reconcile(t, f, its, nil, now))
		f.LastHuman = now.Add(time.Hour)
		cs := task.Activity(f, []task.Task{e}, now.Add(2*time.Hour))
		if !change {
			if len(cs) != 0 {
				t.Fatalf("activity moved a muted entry: %+v", cs)
			}
			continue
		}
		if len(cs) != 1 || cs[0].Task.State != task.Inbox || !cs[0].Task.Changed {
			t.Fatalf("activity: %+v", cs)
		}
		if again := task.Reconcile(f, its, me, []task.Task{cs[0].Task}, now.Add(3*time.Hour)); len(again) != 0 {
			t.Fatalf("the woken entry was muted again: %+v", again)
		}
	}
}

func TestUnmuteRestoresTheState(t *testing.T) {
	mute := func(f *task.UnitFacts) { f.Mute = &task.Mute{} }
	its := []items.Item{item("a", owedBy(ana))}
	e := only(t, reconcile(t, facts(mute), its, nil, now))
	if e.State != task.Scheduled {
		t.Fatalf("muted: %+v", e)
	}
	e = only(t, reconcile(t, facts(), its, []task.Task{e}, now))
	if e.State != task.Waiting || e.Snooze != (task.Snooze{}) {
		t.Fatalf("unmuted: %+v", e)
	}
	lapsed := func(f *task.UnitFacts) { f.Mute = &task.Mute{Until: now.Add(-time.Minute)} }
	if es := reconcile(t, facts(lapsed), its, nil, now); es[0].State != task.Waiting {
		t.Fatalf("a lapsed mute: %+v", es[0])
	}
}

func TestDeadlineStars(t *testing.T) {
	e := task.Task{State: task.Next, Deadline: now}
	f, ok := task.AutoFocus(e, 2, now)
	if !ok || f != (task.Focus{On: true, Auto: task.FocusDeadline, At: now}) {
		t.Fatalf("focus %+v %v", f, ok)
	}
	e.Deadline = now.Add(time.Minute)
	if _, ok := task.AutoFocus(e, 2, now); ok {
		t.Fatal("a future deadline starred")
	}
	e.Focus = f
	if f, ok := task.AutoFocus(e, 2, now); !ok || f.On {
		t.Fatalf("a moved deadline kept its star: %+v %v", f, ok)
	}
	e.Deadline, e.Focus = now.Add(-time.Hour), task.Focus{At: now.Add(-time.Minute)}
	if _, ok := task.AutoFocus(e, 2, now); ok {
		t.Fatal("a star the user cleared came back")
	}
}

func TestStaleCountsWorkingDays(t *testing.T) {
	friday := time.Date(2026, 10, 16, 10, 0, 0, 0, brt)
	e := task.Task{State: task.Waiting, Kind: task.KindWaiting, Waiting: &task.WaitingOn{On: []items.Person{ana}, Since: friday}}
	tuesday := time.Date(2026, 10, 20, 10, 1, 0, 0, brt)
	if f, ok := task.AutoFocus(e, 2, tuesday); !ok || f.Auto != task.FocusStale {
		t.Fatalf("tuesday: %+v %v", f, ok)
	}
	if _, ok := task.AutoFocus(e, 2, tuesday.AddDate(0, 0, -1)); ok {
		t.Fatal("monday starred")
	}
	e.Waiting.LastNudge = friday.Add(time.Hour)
	if _, ok := task.AutoFocus(e, 2, tuesday); ok {
		t.Fatal("a nudged entry starred")
	}
}

func TestUserStarsAreNotCleared(t *testing.T) {
	e := task.Task{State: task.Next, Focus: task.Focus{On: true, At: now}}
	if f, ok := task.AutoFocus(e, 2, now); ok || !f.On {
		t.Fatalf("focus %+v %v", f, ok)
	}
}

func TestActivityReinboxesWithChangedMark(t *testing.T) {
	seen := now.Add(-time.Hour)
	f := facts(func(f *task.UnitFacts) { f.LastHuman = now })
	have := []task.Task{
		{ID: "e-sched", State: task.Scheduled, Snooze: task.Snooze{OnActivity: true, Return: task.Next}, SeenAt: seen},
		{ID: "e-date", State: task.Scheduled, Snooze: task.Snooze{Until: now.Add(time.Hour), Return: task.Next}, SeenAt: seen},
		{ID: "e-wait", State: task.Waiting, SeenAt: seen},
		{ID: "e-next", State: task.Next, SeenAt: seen},
		{ID: "e-fresh", State: task.Waiting, SeenAt: now},
	}
	var got []task.ID
	for _, c := range task.Activity(f, have, now) {
		if c.Task.State != task.Inbox || !c.Task.Changed || c.Op != task.OpReinbox || c.Task.Snooze != (task.Snooze{}) {
			t.Errorf("change %+v", c)
		}
		got = append(got, c.Task.ID)
	}
	if want := []task.ID{"e-sched", "e-date", "e-wait"}; !slices.Equal(got, want) {
		t.Fatalf("reinboxed %v, want %v", got, want)
	}
}

func TestOwnActivityDoesNotReinbox(t *testing.T) {
	seen := now.Add(-time.Hour)
	have := []task.Task{{ID: "e-wait", State: task.Waiting, SeenAt: seen, Created: now.Add(-48 * time.Hour)}}
	for _, last := range []time.Time{{}, seen.Add(-time.Minute)} {
		f := facts(func(f *task.UnitFacts) { f.LastHuman = last })
		if cs := task.Activity(f, have, now); len(cs) != 0 {
			t.Fatalf("last human %v: changes %+v", last, cs)
		}
	}
}

func TestEmptyKeyIsNeverMe(t *testing.T) {
	blank := task.Me{Keys: []string{"", "github:me"}}
	it := item("a", func(it *items.Item) { it.WaitingOn, it.Requester = []items.Person{{Name: "someone"}}, items.Person{} })
	if cs := task.Reconcile(facts(), []items.Item{it}, blank, nil, now); len(cs) != 0 {
		t.Fatalf("an unknown person counted as me: %+v", cs)
	}
}

func TestWakeReturnsToItsState(t *testing.T) {
	e := task.Task{State: task.Scheduled, Snooze: task.Snooze{Until: now, Return: task.Next}}
	got, ok := task.Wake(e, now)
	if !ok || got.State != task.Next || got.Snooze != (task.Snooze{}) {
		t.Fatalf("woke %+v %v", got, ok)
	}
	if _, ok := task.Wake(e, now.Add(-time.Second)); ok {
		t.Fatal("woke early")
	}
	e.Snooze = task.Snooze{OnActivity: true}
	if _, ok := task.Wake(e, now); ok {
		t.Fatal("woke an entry with no date")
	}
}

func TestMutedUnitActivitySparesUserSchedule(t *testing.T) {
	f := facts(func(f *task.UnitFacts) { f.Mute, f.LastHuman = &task.Mute{Until: now.Add(time.Hour)}, now })
	have := []task.Task{{ID: "e-mine", State: task.Scheduled, Snooze: task.Snooze{OnActivity: true, Return: task.Next}, Created: now.Add(-time.Hour)}}
	if cs := task.Activity(f, have, now); len(cs) != 0 {
		t.Fatalf("activity on a muted unit moved %+v", cs)
	}
	if cs := task.Reconcile(f, nil, me, have, now); len(cs) != 0 {
		t.Fatalf("the mute took over a user's schedule: %+v", cs)
	}
}
