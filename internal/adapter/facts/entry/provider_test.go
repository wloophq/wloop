package factentry_test

import (
	"context"
	"path/filepath"
	"slices"
	"sort"
	"testing"
	"time"

	factentry "github.com/wloophq/wloop/internal/adapter/facts/entry"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/core/automation"
	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/domain/card"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
)

var brt = time.FixedZone("BRT", -3*60*60)

// monday is 10:00 BRT on a Monday.
var monday = time.Date(2026, 3, 2, 10, 0, 0, 0, brt)

var (
	chatA = unit.Unit{Provider: "chat", Key: "T1/C1/100.1"}
	chatB = unit.Unit{Provider: "chat", Key: "T1/C1/200.2"}
	mailX = unit.Unit{Provider: "mail", Key: "m-1"}
)

type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

type env struct {
	t     *testing.T
	db    *sqlite.DB
	clock *clock
	p     *factentry.Provider
	s     factentry.Settings
	ended map[unit.Unit]bool
	turns map[unit.Unit]int
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "s.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e := &env{t: t, db: db, clock: &clock{now: monday}, s: factentry.Settings{StaleDays: 2, RenudgeAfter: 72 * time.Hour}, ended: map[unit.Unit]bool{}, turns: map[unit.Unit]int{}}
	e.p = factentry.New(factentry.Deps{
		Entries: db.Entries(), Items: db.Items(), Drafts: db.Drafts(),
		Cards:    coregtd.CardFacts{Cards: db.Cards()},
		Enabled:  func(p unit.ProviderID) bool { return p != mailX.Provider },
		Settings: func() factentry.Settings { return e.s },
		Ended:    func(_ context.Context, u unit.Unit) (bool, error) { return e.ended[u], nil },
		Turns:    func(_ context.Context, u unit.Unit) (int, error) { return e.turns[u], nil },
		Clock:    e.clock,
	})
	return e
}

func (e *env) entry(en gtd.Entry) gtd.Entry {
	e.t.Helper()
	if en.Origin == "" {
		en.Origin = gtd.OriginUser
	}
	if en.Created.IsZero() {
		en.Created, en.Updated = monday.Add(-time.Hour), monday.Add(-time.Hour)
	}
	op := gtd.OpCreate
	if _, err := e.db.Entries().Entry(context.Background(), en.ID); err == nil {
		op = gtd.OpUpdate
	}
	if err := e.db.Entries().Apply(context.Background(), []gtd.Change{{Op: op, Entry: en}}); err != nil {
		e.t.Fatal(err)
	}
	return en
}

func (e *env) item(u unit.Unit, id string, s items.State, created time.Time, waitingOn ...items.Person) items.Item {
	e.t.Helper()
	it := items.Item{ID: id, Unit: u, Kind: items.Request, Summary: "s " + id, Origin: unit.Anchor{Unit: u, Ref: "m:" + id},
		Requester: items.Person{Key: "chat:U9"}, Basis: items.NoBasis, State: s, Confidence: items.High,
		Provenance: items.Extracted, WaitingOn: waitingOn, CreatedAt: created, UpdatedAt: created}
	if s == items.Resolved {
		it.Resolution = items.Done
	}
	if err := e.db.Items().Commit(context.Background(), coreitems.Commit{Items: []items.Item{it}}); err != nil {
		e.t.Fatal(err)
	}
	return it
}

func (e *env) facts(id gtd.ID) workflow.Facts {
	e.t.Helper()
	f, err := e.p.Facts(context.Background(), gtd.UnitOf(id))
	if err != nil {
		e.t.Fatalf("facts of %s: %v", id, err)
	}
	return f
}

func waiting(id gtd.ID, u unit.Unit, since time.Time, its ...items.Item) gtd.Entry {
	en := gtd.Entry{ID: id, Kind: gtd.KindWaiting, State: gtd.Waiting, Title: "w", Units: []unit.Unit{u},
		Waiting: &gtd.WaitingOn{On: []items.Person{{Key: "chat:U1"}}, Since: since}}
	for _, it := range its {
		en.Items = append(en.Items, gtd.ItemRef{Unit: it.Unit, Item: it.ID})
	}
	return en
}

func TestEntryListsTheOpenEntriesOfEnabledPlugins(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.entry(waiting("e-01", chatA, monday))
	e.entry(gtd.Entry{ID: "e-02", Kind: gtd.KindAction, State: gtd.Next, Title: "a", Units: []unit.Unit{chatB}})
	e.entry(gtd.Entry{ID: "e-03", Kind: gtd.KindCapture, State: gtd.Inbox, Title: "c"})
	e.entry(gtd.Entry{ID: "e-04", Kind: gtd.KindAction, State: gtd.Someday, Title: "m", Units: []unit.Unit{mailX}})
	e.entry(gtd.Entry{ID: "e-05", Kind: gtd.KindAction, State: gtd.Done, Title: "d", Units: []unit.Unit{chatA}})
	e.entry(gtd.Entry{ID: "e-06", Kind: gtd.KindAction, State: gtd.Trash, Title: "t", Units: []unit.Unit{chatA}})
	e.entry(gtd.Entry{ID: "e-07", Kind: gtd.KindReference, State: gtd.Reference, Title: "r", Units: []unit.Unit{chatA}})
	got, err := e.p.List(context.Background(), unit.Entry)
	if err != nil {
		t.Fatal(err)
	}
	want := []unit.Unit{gtd.UnitOf("e-01"), gtd.UnitOf("e-02"), gtd.UnitOf("e-03"), gtd.UnitOf("e-07")}
	if !slices.Equal(got, want) {
		t.Fatalf("listed %v, want %v", got, want)
	}
	for _, id := range []gtd.ID{"e-04", "e-05", "e-06", "e-99"} {
		if _, err := e.p.Facts(context.Background(), gtd.UnitOf(id)); err != automation.ErrUnitGone {
			t.Errorf("facts of %s: %v, want ErrUnitGone", id, err)
		}
	}
}

func TestEntryFactsAreTheSlotsFacts(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.entry(waiting("e-01", chatA, monday))
	f := e.facts("e-01")
	var want, got []string
	for _, d := range builtin.Facts {
		if d.Slot == "entry" {
			want = append(want, d.Name)
		}
	}
	for k := range f {
		got = append(got, k)
	}
	sort.Strings(want)
	sort.Strings(got)
	if !slices.Equal(got, want) {
		t.Fatalf("facts %v, want the entry slot's %v", got, want)
	}
	env := builtin.WorkflowEnv("entry")
	for k, v := range f {
		if typeOf(v) != env[k] {
			t.Errorf("%s = %#v, want a %s", k, v, env[k])
		}
	}
}

func typeOf(v any) expr.Type {
	switch v.(type) {
	case bool:
		return expr.Bool
	case int:
		return expr.Int
	case string:
		return expr.String
	case []string:
		return expr.List
	case time.Duration:
		return expr.Duration
	case time.Time:
		return expr.Time
	}
	return 0
}

func TestEntryFactsReadTheEntryItsItemsAndItsUnitsCard(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	owed := e.item(chatA, "i1", items.Open, monday.Add(-48*time.Hour), items.Person{Key: "chat:U1"})
	e.item(chatA, "i2", items.Open, monday.Add(-48*time.Hour))
	e.item(chatA, "i3", items.Resolved, monday.Add(-48*time.Hour))
	e.item(chatB, "i4", items.Open, monday.Add(-48*time.Hour))
	en := waiting("e-01", chatA, time.Date(2026, 2, 25, 9, 0, 0, 0, brt), owed)
	en.Waiting.LastNudge = monday.Add(-80 * time.Hour)
	en.Waiting.On = append(en.Waiting.On, items.Person{Key: "chat:U2"})
	e.entry(en)
	if _, err := e.db.Cards().Save(ctx, card.Card{Unit: chatA, Title: "t", State: "thread", StartedByMe: true, ClosurePosted: true}, monday); err != nil {
		t.Fatal(err)
	}
	f := e.facts("e-01")
	want := workflow.Facts{
		"entry.id": "e-01", "entry.kind": "waiting", "entry.state": "waiting",
		"entry.waiting_days": 3, "entry.since_nudge": 80 * time.Hour,
		"entry.unit": "chat:T1/C1/100.1", "entry.unit_provider": "chat",
		"entry.unit_started_by_me": true, "entry.unit_closure_posted": true,
		"entry.items_open": 2, "entry.items_resolved": 1, "entry.items_owed": 1, "entry.muted": false,
		"entry.nudge_due_at": monday.Add(-8 * time.Hour),
	}
	for k, v := range want {
		if got := f[k]; !equal(got, v) {
			t.Errorf("%s = %#v, want %#v", k, got, v)
		}
	}
	if got := f["entry.waiting_on"].([]string); !slices.Equal(got, []string{"chat:U1", "chat:U2"}) {
		t.Errorf("waiting_on %v", got)
	}
}

func equal(a, b any) bool {
	if at, ok := a.(time.Time); ok {
		bt, ok := b.(time.Time)
		return ok && at.Equal(bt)
	}
	return a == b
}

func TestEntryDueIsTheNextNudgeOrResurfaceTime(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	i1 := e.item(chatA, "i1", items.Open, monday)
	e.entry(waiting("e-01", chatA, monday, i1))
	soon := waiting("e-02", chatB, monday)
	soon.Waiting.Resurface = monday.Add(5 * time.Hour)
	e.entry(soon)
	nudged := waiting("e-03", chatA, monday.Add(-10*24*time.Hour))
	nudged.Waiting.LastNudge = monday.Add(-71 * time.Hour)
	e.entry(nudged)
	list := func() time.Time {
		t.Helper()
		if _, err := e.p.List(context.Background(), unit.Entry); err != nil {
			t.Fatal(err)
		}
		return e.p.Due()
	}
	if got, want := list(), monday.Add(time.Hour); !got.Equal(want) {
		t.Fatalf("due %v, want e-03's renudge %v", got, want)
	}
	e.clock.now = monday.Add(time.Hour)
	if got, want := list(), monday.Add(5*time.Hour); !got.Equal(want) {
		t.Fatalf("due %v, want e-02's resurface %v", got, want)
	}
	e.clock.now = monday.Add(5 * time.Hour)
	if got, want := list(), time.Date(2026, 3, 4, 10, 0, 0, 0, brt); !got.Equal(want) {
		t.Fatalf("due %v, want e-01's two working days %v", got, want)
	}
	if f := e.facts("e-02"); !f["entry.nudge_due_at"].(time.Time).Equal(monday.Add(5 * time.Hour)) {
		t.Errorf("e-02 at its resurface date: nudge_due_at %v", f["entry.nudge_due_at"])
	}
	if f := e.facts("e-01"); !f["entry.nudge_due_at"].(time.Time).IsZero() {
		t.Errorf("e-01 is not due yet: nudge_due_at %v", f["entry.nudge_due_at"])
	}
}

func TestAnEntrysOwnerIsItsUnit(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	i1 := e.item(chatB, "i1", items.Open, monday)
	e.entry(waiting("e-01", chatA, monday))
	e.entry(gtd.Entry{ID: "e-02", Kind: gtd.KindAction, State: gtd.Next, Title: "a", Items: []gtd.ItemRef{{Unit: i1.Unit, Item: i1.ID}}})
	e.entry(gtd.Entry{ID: "e-03", Kind: gtd.KindCapture, State: gtd.Inbox, Title: "c"})
	e.entry(gtd.Entry{ID: "e-04", Kind: gtd.KindCapture, State: gtd.Next, Title: "c", Units: []unit.Unit{chatB, chatA}})
	for id, want := range map[gtd.ID][]unit.Unit{"e-01": {chatA}, "e-02": {chatB}, "e-03": nil, "e-04": {chatB, chatA}, "e-99": nil} {
		got, err := e.p.Owners(context.Background(), gtd.UnitOf(id))
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("owners of %s: %v, %v; want %v", id, got, err, want)
		}
	}
}

func TestEntrySettledAtIsTheWorkflowsOwn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	e.entry(waiting("e-01", chatA, monday))
	put := func(wf string, s draft.State, at time.Time) {
		t.Helper()
		d := draft.Draft{Unit: chatA, Entry: "e-01", Kind: draft.Nudge, Purpose: "nudge", Delivery: draft.Message, Text: "x", Original: "x",
			Origin: draft.Origin{Kind: draft.OriginAuto, Workflow: wf, Rule: wf, Key: "e-01:chat:U1"}, Approval: draft.Approval{Mode: draft.Wait},
			State: s, CreatedAt: at, UpdatedAt: at}
		if s == draft.Discarded {
			d.Reason = draft.WithdrawMoot
		}
		if _, err := e.db.Drafts().Insert(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	put("nudge-a", draft.Sent, monday.Add(-3*time.Hour))
	put("nudge-a", draft.Discarded, monday.Add(-2*time.Hour))
	put("nudge-a", draft.Proposed, monday.Add(-time.Hour))
	put("nudge-b", draft.Sent, monday.Add(-30*time.Minute))
	f := e.facts("e-01")
	scoped, err := e.p.Scope(ctx, "nudge-a", gtd.UnitOf("e-01"), f)
	if err != nil {
		t.Fatal(err)
	}
	if got := scoped["entry.settled_at"].(time.Time); !got.Equal(monday.Add(-2 * time.Hour)) {
		t.Errorf("nudge-a settled_at %v, want its withdrawn draft's", got)
	}
	if got := f["entry.settled_at"].(time.Time); !got.IsZero() {
		t.Errorf("Scope wrote the unit's facts: %v", got)
	}
	if fresh, _ := e.p.Scope(ctx, "nudge-c", gtd.UnitOf("e-01"), f); !fresh["entry.settled_at"].(time.Time).IsZero() {
		t.Errorf("a workflow with no drafts: %v", fresh["entry.settled_at"])
	}
}

func TestAnEntryThatLeftItsListStillHasEventFacts(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.entry(gtd.Entry{ID: "e-05", Kind: gtd.KindAction, State: gtd.Done, Title: "d", Units: []unit.Unit{chatA}})
	e.entry(gtd.Entry{ID: "e-04", Kind: gtd.KindAction, State: gtd.Someday, Title: "m", Units: []unit.Unit{mailX}})
	f, err := e.p.EventFacts(context.Background(), gtd.UnitOf("e-05"))
	if err != nil || f["entry.state"] != "done" || f["entry.unit"] != chatA.String() {
		t.Fatalf("event facts of a done entry: %v %v", f, err)
	}
	for _, id := range []gtd.ID{"e-04", "e-99"} {
		if _, err := e.p.EventFacts(context.Background(), gtd.UnitOf(id)); err != automation.ErrUnitGone {
			t.Errorf("event facts of %s: %v, want ErrUnitGone", id, err)
		}
	}
}

var docY = unit.Unit{Provider: "doc", Key: "d-1"}

func TestEntryFactsSpanEveryUnitInOrder(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.item(chatA, "i1", items.Open, monday)
	owed := e.item(chatB, "i2", items.Open, monday, items.Person{Key: "chat:U1"})
	e.item(chatB, "i3", items.Resolved, monday)
	onDoc := e.item(docY, "i4", items.Open, monday)
	en := gtd.Entry{ID: "e-01", Kind: gtd.KindCapture, State: gtd.Next, Title: "ship it", Units: []unit.Unit{chatA, chatB},
		Items: []gtd.ItemRef{{Unit: owed.Unit, Item: owed.ID}, {Unit: onDoc.Unit, Item: onDoc.ID}}}
	e.entry(en)
	f := e.facts("e-01")
	if f["entry.unit"] != chatA.String() || f["entry.unit_provider"] != "chat" {
		t.Errorf("the first unit: %v %v", f["entry.unit"], f["entry.unit_provider"])
	}
	if got := f["entry.units"].([]string); !slices.Equal(got, []string{chatA.String(), chatB.String(), docY.String()}) {
		t.Errorf("units %v, want the attached ones in order, then the one only an item names", got)
	}
	if got := f["entry.unit_providers"].([]string); !slices.Equal(got, []string{"chat", "doc"}) {
		t.Errorf("unit_providers %v", got)
	}
	if f["entry.items_open"] != 3 || f["entry.items_resolved"] != 1 || f["entry.items_owed"] != 2 {
		t.Errorf("items open %v, resolved %v, owed %v; want 3, 1, 2 over every unit", f["entry.items_open"], f["entry.items_resolved"], f["entry.items_owed"])
	}
}

func TestEntryUnitsEndedWhenEveryUnitEnded(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.entry(gtd.Entry{ID: "e-01", Kind: gtd.KindCapture, State: gtd.Next, Title: "none"})
	e.entry(gtd.Entry{ID: "e-02", Kind: gtd.KindCapture, State: gtd.Next, Title: "two", Units: []unit.Unit{chatA, chatB}})
	if f := e.facts("e-01"); f["entry.units_ended"] != false {
		t.Error("an entry with no unit has its units ended")
	}
	e.ended[chatA] = true
	if f := e.facts("e-02"); f["entry.units_ended"] != false {
		t.Error("an entry with a unit still going has its units ended")
	}
	e.ended[chatB] = true
	if f := e.facts("e-02"); f["entry.units_ended"] != true {
		t.Error("an entry whose every unit ended has not")
	}
}

func TestEntryTurnsCountsTheTurnsOnTheEntryItself(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.entry(gtd.Entry{ID: "e-01", Kind: gtd.KindCapture, State: gtd.Next, Title: "tracked", Units: []unit.Unit{chatA}})
	e.turns[chatA] = 3
	if f := e.facts("e-01"); f["entry.turns"] != 0 {
		t.Errorf("entry.turns %v, want the turns on its unit left out", f["entry.turns"])
	}
	e.turns[gtd.UnitOf("e-01")] = 2
	if f := e.facts("e-01"); f["entry.turns"] != 2 {
		t.Errorf("entry.turns %v, want 2", f["entry.turns"])
	}
}

func TestEntryCapturedByUser(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.entry(gtd.Entry{ID: "e-01", Kind: gtd.KindCapture, State: gtd.Inbox, Title: "mine"})
	e.entry(gtd.Entry{ID: "e-02", Kind: gtd.KindAction, State: gtd.Inbox, Title: "derived", Origin: gtd.OriginDerived, Units: []unit.Unit{chatA}})
	e.entry(gtd.Entry{ID: "e-03", Kind: gtd.KindMessage, State: gtd.Inbox, Title: "a message", Origin: gtd.OriginMessage, Units: []unit.Unit{chatA}})
	for id, want := range map[gtd.ID]bool{"e-01": true, "e-02": false, "e-03": false} {
		if got := e.facts(id)["entry.captured_by_user"]; got != want {
			t.Errorf("%s: captured_by_user %v, want %v", id, got, want)
		}
	}
}
