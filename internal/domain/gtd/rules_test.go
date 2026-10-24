package gtd_test

import (
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var (
	brt, _ = time.LoadLocation("America/Sao_Paulo")
	now    = time.Date(2026, 10, 20, 10, 0, 0, 0, brt)
	me     = gtd.Me{Keys: []string{"github:me", "slack:U000ME"}}
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

func facts(mod ...func(*gtd.UnitFacts)) gtd.UnitFacts {
	f := gtd.UnitFacts{Unit: pr}
	for _, m := range mod {
		m(&f)
	}
	return f
}

// apply folds cs into have by id, as the store would.
func apply(have []gtd.Entry, cs []gtd.Change) []gtd.Entry {
	out := slices.Clone(have)
	for _, c := range cs {
		i := slices.IndexFunc(out, func(e gtd.Entry) bool { return e.ID == c.Entry.ID })
		if i < 0 {
			out = append(out, c.Entry)
		} else {
			out[i] = c.Entry
		}
	}
	return out
}

func reconcile(t *testing.T, f gtd.UnitFacts, its []items.Item, have []gtd.Entry, at time.Time) []gtd.Entry {
	t.Helper()
	return apply(have, gtd.Reconcile(f, its, me, have, at))
}

func only(t *testing.T, es []gtd.Entry) gtd.Entry {
	t.Helper()
	if len(es) != 1 {
		t.Fatalf("%d entries, want 1: %+v", len(es), es)
	}
	return es[0]
}

func TestOpenItemOwedByMeIsAnAction(t *testing.T) {
	its := []items.Item{item("i1", func(it *items.Item) { it.Contexts = []string{"@reply"} })}
	e := only(t, reconcile(t, facts(), its, nil, now))
	if e.Kind != gtd.KindAction || e.State != gtd.Inbox || !slices.Equal(e.Contexts, []string{"@reply"}) || e.Origin != gtd.OriginDerived {
		t.Fatalf("entry %+v", e)
	}
	if !slices.Equal(e.Items, []gtd.ItemRef{{Unit: pr, Item: "i1"}}) || !slices.Equal(e.Units, []unit.Unit{pr}) {
		t.Fatalf("refs %+v units %+v", e.Items, e.Units)
	}
	e.State, e.Contexts = gtd.Next, []string{"@decide"}
	its[0].Summary = "do it better"
	got := only(t, reconcile(t, facts(), its, []gtd.Entry{e}, now.Add(time.Hour)))
	if got.State != gtd.Next || got.Title != "do it better" || !slices.Equal(got.Contexts, []string{"@decide"}) {
		t.Fatalf("after the item changed: %+v", got)
	}
}

func TestDerivedItemLandsWhereThePluginSays(t *testing.T) {
	its := []items.Item{
		item("next", func(it *items.Item) { it.Initial = "next" }),
		item("inbox", func(it *items.Item) { it.Initial = "inbox" }),
		item("none"),
	}
	got := map[string]gtd.State{}
	for _, e := range reconcile(t, facts(), its, nil, now) {
		got[e.Items[0].Item] = e.State
	}
	if want := map[string]gtd.State{"next": gtd.Next, "inbox": gtd.Inbox, "none": gtd.Inbox}; !mapsEqual(got, want) {
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
	if e.Kind != gtd.KindWaiting || e.State != gtd.Waiting || len(e.Items) != 3 || e.Waiting == nil || e.Waiting.On[0].Key != ana.Key {
		t.Fatalf("entry %+v", e)
	}
	its = append(its, item("d", owedBy(bob)))
	es := reconcile(t, facts(), its, []gtd.Entry{e}, now)
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
	cares := func(f *gtd.UnitFacts) { f.Cares = true }
	if e := only(t, reconcile(t, facts(cares), its, nil, now)); e.Kind != gtd.KindWaiting {
		t.Fatalf("with care: %+v", e)
	}
}

func TestCloseActionForMyResolvedUnit(t *testing.T) {
	mine := func(f *gtd.UnitFacts) { f.StartedByMe = true }
	its := []items.Item{item("a", resolved), item("b", owedBy(ana), resolved)}
	e := only(t, reconcile(t, facts(mine), its, nil, now))
	if e.Kind != gtd.KindClose || e.State != gtd.Inbox {
		t.Fatalf("entry %+v", e)
	}
	posted := func(f *gtd.UnitFacts) { f.ClosurePosted = true }
	if got := only(t, reconcile(t, facts(mine, posted), its, []gtd.Entry{e}, now)); got.State != gtd.Done {
		t.Fatalf("after the closure: %+v", got)
	}
	if es := reconcile(t, facts(mine), its[:1:1], nil, now); len(es) != 1 {
		t.Fatalf("one resolved item: %+v", es)
	}
	if es := reconcile(t, facts(mine), append(its, item("c")), nil, now); len(es) != 1 || es[0].Kind != gtd.KindAction {
		t.Fatalf("an open item left: %+v", es)
	}
}

func TestResolvedItemsResolveTheEntry(t *testing.T) {
	its := []items.Item{item("a", owedBy(ana)), item("b", owedBy(ana))}
	e := only(t, reconcile(t, facts(), its, nil, now))
	resolved(&its[0])
	e = only(t, reconcile(t, facts(), its, []gtd.Entry{e}, now))
	if e.State != gtd.Waiting || !slices.Equal(e.Items, []gtd.ItemRef{{Unit: pr, Item: "b"}}) {
		t.Fatalf("one resolved: %+v", e)
	}
	resolved(&its[1])
	e = only(t, reconcile(t, facts(), its, []gtd.Entry{e}, now))
	if e.State != gtd.Done || len(e.Items) != 0 {
		t.Fatalf("all resolved: %+v", e)
	}
}

func TestOnlyItemsCreateEntries(t *testing.T) {
	f := facts(func(f *gtd.UnitFacts) { f.StartedByMe, f.Cares, f.LastHuman = true, true, now })
	if es := reconcile(t, f, nil, nil, now); len(es) != 0 {
		t.Fatalf("entries %+v", es)
	}
}

func TestDismissedStaysDismissedUntilReopen(t *testing.T) {
	its := []items.Item{item("a", owedBy(ana)), item("b", owedBy(ana))}
	e := only(t, reconcile(t, facts(), its, nil, now))
	e.State = gtd.Trash
	if cs := gtd.Reconcile(facts(), its, me, []gtd.Entry{e}, now); len(cs) != 0 {
		t.Fatalf("changes %+v", cs)
	}
	resolved(&its[0])
	e = only(t, reconcile(t, facts(), its, []gtd.Entry{e}, now))
	if e.State != gtd.Trash {
		t.Fatalf("after a resolve: %+v", e)
	}
	its[0].State = "open"
	e = only(t, reconcile(t, facts(), its, []gtd.Entry{e}, now))
	if e.State != gtd.Inbox || len(e.Items) != 2 {
		t.Fatalf("after the reopen: %+v", e)
	}
}

func TestReconcileIsIdempotent(t *testing.T) {
	mute := func(f *gtd.UnitFacts) { f.Mute = &gtd.Mute{Until: now.Add(48 * time.Hour)}; f.Cares = true }
	its := []items.Item{item("a"), item("b", owedBy(ana)), item("c", owedBy(bob), func(it *items.Item) { it.Deadline = "2026-10-30" })}
	first := reconcile(t, facts(mute), its, nil, now)
	cs := gtd.Reconcile(facts(mute), its, me, first, now.Add(time.Minute))
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

func TestMutedUnitEntryIsSnoozed(t *testing.T) {
	until := now.Add(72 * time.Hour)
	mute := func(f *gtd.UnitFacts) { f.Mute = &gtd.Mute{Until: until} }
	its := []items.Item{item("a", func(it *items.Item) { it.Initial = "next" })}
	e := only(t, reconcile(t, facts(mute), its, nil, now))
	want := gtd.Snooze{Until: until, Return: gtd.Next, Mute: true}
	if e.State != gtd.Scheduled || e.Snooze != want {
		t.Fatalf("new entry %+v", e)
	}
	plain := only(t, reconcile(t, facts(), its, nil, now))
	got := only(t, reconcile(t, facts(mute), its, []gtd.Entry{plain}, now))
	if got.State != gtd.Scheduled || got.Snooze != want {
		t.Fatalf("existing entry %+v", got)
	}
}

func TestMuteUntilStateChangeWakesOnActivity(t *testing.T) {
	its := []items.Item{item("a")}
	for _, change := range []bool{true, false} {
		f := facts(func(f *gtd.UnitFacts) { f.Mute = &gtd.Mute{UntilStateChange: change} })
		e := only(t, reconcile(t, f, its, nil, now))
		f.LastHuman = now.Add(time.Hour)
		cs := gtd.Activity(f, []gtd.Entry{e}, now.Add(2*time.Hour))
		if !change {
			if len(cs) != 0 {
				t.Fatalf("activity moved a muted entry: %+v", cs)
			}
			continue
		}
		if len(cs) != 1 || cs[0].Entry.State != gtd.Inbox || !cs[0].Entry.Changed {
			t.Fatalf("activity: %+v", cs)
		}
		if again := gtd.Reconcile(f, its, me, []gtd.Entry{cs[0].Entry}, now.Add(3*time.Hour)); len(again) != 0 {
			t.Fatalf("the woken entry was muted again: %+v", again)
		}
	}
}

func TestUnmuteRestoresTheState(t *testing.T) {
	mute := func(f *gtd.UnitFacts) { f.Mute = &gtd.Mute{} }
	its := []items.Item{item("a", owedBy(ana))}
	e := only(t, reconcile(t, facts(mute), its, nil, now))
	if e.State != gtd.Scheduled {
		t.Fatalf("muted: %+v", e)
	}
	e = only(t, reconcile(t, facts(), its, []gtd.Entry{e}, now))
	if e.State != gtd.Waiting || e.Snooze != (gtd.Snooze{}) {
		t.Fatalf("unmuted: %+v", e)
	}
	lapsed := func(f *gtd.UnitFacts) { f.Mute = &gtd.Mute{Until: now.Add(-time.Minute)} }
	if es := reconcile(t, facts(lapsed), its, nil, now); es[0].State != gtd.Waiting {
		t.Fatalf("a lapsed mute: %+v", es[0])
	}
}

func TestDeadlineStars(t *testing.T) {
	e := gtd.Entry{State: gtd.Next, Deadline: now}
	f, ok := gtd.AutoFocus(e, 2, now)
	if !ok || f != (gtd.Focus{On: true, Auto: gtd.FocusDeadline, At: now}) {
		t.Fatalf("focus %+v %v", f, ok)
	}
	e.Deadline = now.Add(time.Minute)
	if _, ok := gtd.AutoFocus(e, 2, now); ok {
		t.Fatal("a future deadline starred")
	}
	e.Focus = f
	if f, ok := gtd.AutoFocus(e, 2, now); !ok || f.On {
		t.Fatalf("a moved deadline kept its star: %+v %v", f, ok)
	}
	e.Deadline, e.Focus = now.Add(-time.Hour), gtd.Focus{At: now.Add(-time.Minute)}
	if _, ok := gtd.AutoFocus(e, 2, now); ok {
		t.Fatal("a star the user cleared came back")
	}
}

func TestStaleCountsWorkingDays(t *testing.T) {
	friday := time.Date(2026, 10, 16, 10, 0, 0, 0, brt)
	e := gtd.Entry{State: gtd.Waiting, Kind: gtd.KindWaiting, Waiting: &gtd.WaitingOn{On: []items.Person{ana}, Since: friday}}
	tuesday := time.Date(2026, 10, 20, 10, 1, 0, 0, brt)
	if f, ok := gtd.AutoFocus(e, 2, tuesday); !ok || f.Auto != gtd.FocusStale {
		t.Fatalf("tuesday: %+v %v", f, ok)
	}
	if _, ok := gtd.AutoFocus(e, 2, tuesday.AddDate(0, 0, -1)); ok {
		t.Fatal("monday starred")
	}
	e.Waiting.LastNudge = friday.Add(time.Hour)
	if _, ok := gtd.AutoFocus(e, 2, tuesday); ok {
		t.Fatal("a nudged entry starred")
	}
}

func TestUserStarsAreNotCleared(t *testing.T) {
	e := gtd.Entry{State: gtd.Next, Focus: gtd.Focus{On: true, At: now}}
	if f, ok := gtd.AutoFocus(e, 2, now); ok || !f.On {
		t.Fatalf("focus %+v %v", f, ok)
	}
}

func TestActivityReinboxesWithChangedMark(t *testing.T) {
	seen := now.Add(-time.Hour)
	f := facts(func(f *gtd.UnitFacts) { f.LastHuman = now })
	have := []gtd.Entry{
		{ID: "e-sched", State: gtd.Scheduled, Snooze: gtd.Snooze{OnActivity: true, Return: gtd.Next}, SeenAt: seen},
		{ID: "e-date", State: gtd.Scheduled, Snooze: gtd.Snooze{Until: now.Add(time.Hour), Return: gtd.Next}, SeenAt: seen},
		{ID: "e-wait", State: gtd.Waiting, SeenAt: seen},
		{ID: "e-next", State: gtd.Next, SeenAt: seen},
		{ID: "e-fresh", State: gtd.Waiting, SeenAt: now},
	}
	var got []gtd.ID
	for _, c := range gtd.Activity(f, have, now) {
		if c.Entry.State != gtd.Inbox || !c.Entry.Changed || c.Op != gtd.OpReinbox || c.Entry.Snooze != (gtd.Snooze{}) {
			t.Errorf("change %+v", c)
		}
		got = append(got, c.Entry.ID)
	}
	if want := []gtd.ID{"e-sched", "e-date", "e-wait"}; !slices.Equal(got, want) {
		t.Fatalf("reinboxed %v, want %v", got, want)
	}
}

func TestOwnActivityDoesNotReinbox(t *testing.T) {
	seen := now.Add(-time.Hour)
	have := []gtd.Entry{{ID: "e-wait", State: gtd.Waiting, SeenAt: seen, Created: now.Add(-48 * time.Hour)}}
	for _, last := range []time.Time{{}, seen.Add(-time.Minute)} {
		f := facts(func(f *gtd.UnitFacts) { f.LastHuman = last })
		if cs := gtd.Activity(f, have, now); len(cs) != 0 {
			t.Fatalf("last human %v: changes %+v", last, cs)
		}
	}
}

func TestEmptyKeyIsNeverMe(t *testing.T) {
	blank := gtd.Me{Keys: []string{"", "github:me"}}
	it := item("a", func(it *items.Item) { it.WaitingOn, it.Requester = []items.Person{{Name: "someone"}}, items.Person{} })
	if cs := gtd.Reconcile(facts(), []items.Item{it}, blank, nil, now); len(cs) != 0 {
		t.Fatalf("an unknown person counted as me: %+v", cs)
	}
}

func TestWakeReturnsToItsState(t *testing.T) {
	e := gtd.Entry{State: gtd.Scheduled, Snooze: gtd.Snooze{Until: now, Return: gtd.Next}}
	got, ok := gtd.Wake(e, now)
	if !ok || got.State != gtd.Next || got.Snooze != (gtd.Snooze{}) {
		t.Fatalf("woke %+v %v", got, ok)
	}
	if _, ok := gtd.Wake(e, now.Add(-time.Second)); ok {
		t.Fatal("woke early")
	}
	e.Snooze = gtd.Snooze{OnActivity: true}
	if _, ok := gtd.Wake(e, now); ok {
		t.Fatal("woke an entry with no date")
	}
}

func TestMutedUnitActivitySparesUserSchedule(t *testing.T) {
	f := facts(func(f *gtd.UnitFacts) { f.Mute, f.LastHuman = &gtd.Mute{Until: now.Add(time.Hour)}, now })
	have := []gtd.Entry{{ID: "e-mine", State: gtd.Scheduled, Snooze: gtd.Snooze{OnActivity: true, Return: gtd.Next}, Created: now.Add(-time.Hour)}}
	if cs := gtd.Activity(f, have, now); len(cs) != 0 {
		t.Fatalf("activity on a muted unit moved %+v", cs)
	}
	if cs := gtd.Reconcile(f, nil, me, have, now); len(cs) != 0 {
		t.Fatalf("the mute took over a user's schedule: %+v", cs)
	}
}
