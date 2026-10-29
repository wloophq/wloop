package facttask_test

import (
	"context"
	"io/fs"
	"path/filepath"
	"slices"
	"sort"
	"testing"
	"time"

	intentload "github.com/wloophq/wloop/internal/intent"

	facttask "github.com/wloophq/wloop/internal/adapter/facts/task"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/gates"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/card"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/wake"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
	"github.com/wloophq/wloop/profiles"
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
	t        *testing.T
	db       *sqlite.DB
	clock    *clock
	p        *facttask.Provider
	s        facttask.Settings
	ended    map[unit.Unit]bool
	turns    map[unit.Unit]int
	repos    map[unit.Unit]string
	sessions []turn.Session
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "s.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e := &env{t: t, db: db, clock: &clock{now: monday}, s: facttask.Settings{StaleDays: 2, RenudgeAfter: 72 * time.Hour}, ended: map[unit.Unit]bool{}, turns: map[unit.Unit]int{}, repos: map[unit.Unit]string{}}
	e.p = facttask.New(facttask.Deps{
		Tasks: db.Tasks(), Items: db.Items(), Drafts: db.Drafts(),
		Cards:    coretasks.CardFacts{Cards: db.Cards()},
		Enabled:  func(p unit.ProviderID) bool { return p != mailX.Provider },
		Settings: func() facttask.Settings { return e.s },
		Ended:    func(_ context.Context, u unit.Unit) (bool, error) { return e.ended[u], nil },
		Turns:    func(_ context.Context, u unit.Unit) (int, error) { return e.turns[u], nil },
		Repo:     func(_ context.Context, u unit.Unit) (string, error) { return e.repos[u], nil },
		Gates:    db.Gates(func() time.Time { return e.clock.now }),
		Sessions: func(context.Context) ([]turn.Session, error) { return e.sessions, nil },
		Clock:    e.clock,
		Props:    func(t task.Task) workflow.Facts { return catalog.PropFacts(t, bundledTasks) },
		Me:       func() task.Me { return task.Me{Keys: []string{"chat:ME"}} },
		OwedBy:   catalog.OwedByOf(func() *intentload.Loaded { return &intentload.Loaded{Tasks: bundledTasks} }),
		Wakes:    db.Wakes(),
	})
	return e
}

var bundledTasks = func() intentload.TasksDef {
	sub, err := fs.Sub(profiles.Default, "default")
	if err != nil {
		panic(err)
	}
	d, problems := intentload.BundledTasks(sub)
	if len(problems) > 0 {
		panic(problems.Error())
	}
	return d
}()

func (e *env) task(en task.Task) task.Task {
	e.t.Helper()
	if en.Origin == "" {
		en.Origin = task.OriginUser
	}
	if en.Created.IsZero() {
		en.Created, en.Updated = monday.Add(-time.Hour), monday.Add(-time.Hour)
	}
	op := task.OpCreate
	if _, err := e.db.Tasks().Task(context.Background(), en.ID); err == nil {
		op = task.OpUpdate
	}
	if err := e.db.Tasks().Apply(context.Background(), []task.Change{{Op: op, Task: en}}); err != nil {
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

func (e *env) facts(id task.ID) workflow.Facts {
	e.t.Helper()
	f, err := e.p.Facts(context.Background(), task.UnitOf(id))
	if err != nil {
		e.t.Fatalf("facts of %s: %v", id, err)
	}
	return f
}

// waiting is a waiting task on u holding its, created at since.
func waiting(id task.ID, u unit.Unit, since time.Time, its ...items.Item) task.Task {
	en := task.Task{ID: id, State: "waiting", Category: task.CategoryWaiting, Title: "w", Units: []unit.Unit{u}, Created: since, Updated: since}
	for _, it := range its {
		en.Items = append(en.Items, task.ItemRef{Unit: it.Unit, Item: it.ID})
	}
	return en
}

func TestTaskListsTheOpenTasksOfEnabledPlugins(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.task(waiting("e-01", chatA, monday))
	e.task(task.Task{ID: "e-02", State: "next", Category: task.CategoryReady, Title: "a", Units: []unit.Unit{chatB}})
	e.task(task.Task{ID: "e-03", State: "inbox", Category: task.CategoryInbox, Title: "c"})
	e.task(task.Task{ID: "e-04", State: "someday", Category: task.CategoryDeferred, Title: "m", Units: []unit.Unit{mailX}})
	e.task(task.Task{ID: "e-05", State: "done", Category: task.CategoryDone, Title: "d", Units: []unit.Unit{chatA}})
	e.task(task.Task{ID: "e-06", State: "trash", Category: task.CategoryDropped, Title: "t", Units: []unit.Unit{chatA}})
	e.task(task.Task{ID: "e-07", State: "reference", Category: task.CategoryDone, Title: "r", Units: []unit.Unit{chatA}})
	got, err := e.p.List(context.Background(), unit.Task)
	if err != nil {
		t.Fatal(err)
	}
	want := []unit.Unit{task.UnitOf("e-01"), task.UnitOf("e-02"), task.UnitOf("e-03")}
	if !slices.Equal(got, want) {
		t.Fatalf("listed %v, want %v", got, want)
	}
	for _, id := range []task.ID{"e-04", "e-05", "e-06", "e-07", "e-99"} {
		if _, err := e.p.Facts(context.Background(), task.UnitOf(id)); err != automation.ErrUnitGone {
			t.Errorf("facts of %s: %v, want ErrUnitGone", id, err)
		}
	}
}

func TestTaskFactsAreTheSlotsFacts(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.task(waiting("e-01", chatA, monday))
	f := e.facts("e-01")
	var want, got []string
	for _, d := range builtin.Facts {
		if d.Slot == "task" {
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
	env := builtin.WorkflowEnv("task")
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

func TestTaskFactsReadTheTaskItsItemsAndItsUnitsCard(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	owed := e.item(chatA, "i1", items.Open, time.Date(2026, 2, 25, 9, 0, 0, 0, brt), items.Person{Key: "chat:U1"}, items.Person{Key: "chat:ME"})
	e.item(chatA, "i2", items.Open, monday.Add(-48*time.Hour))
	e.item(chatA, "i3", items.Resolved, monday.Add(-48*time.Hour))
	e.item(chatB, "i4", items.Open, monday.Add(-48*time.Hour))
	en := waiting("e-01", chatA, monday.Add(-time.Hour), owed)
	en.SetProp(task.PropLastNudge, task.Value{Time: monday.Add(-80 * time.Hour)}, task.WriterWorkflow, monday)
	e.task(en)
	if _, err := e.db.Cards().Save(ctx, card.Card{Unit: chatA, Title: "t", State: "thread", StartedByMe: true, ClosurePosted: true}, monday); err != nil {
		t.Fatal(err)
	}
	f := e.facts("e-01")
	want := workflow.Facts{
		"task.id": "e-01", "task.state": "waiting",
		"task.unit": "chat:T1/C1/100.1", "task.unit_provider": "chat",
		"task.link_started_by_me": true, "task.link_closure_posted": true,
		"task.items_open": 2, "task.items_resolved": 1,
		"task.prop.last_nudge": monday.Add(-80 * time.Hour), "task.owed_by": "me",
		"task.owed_since": time.Date(2026, 2, 25, 9, 0, 0, 0, brt), "task.check_on": time.Time{},
	}
	for k, v := range want {
		if got := f[k]; !equal(got, v) {
			t.Errorf("%s = %#v, want %#v", k, got, v)
		}
	}
	if got := f["task.link_owes"].([]string); !slices.Equal(got, []string{"chat:U1", "me"}) {
		t.Errorf("link_owes %v", got)
	}
}

func TestTheWakeFactsAreTheTasksWake(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.task(waiting("e-01", chatA, monday))
	if f := e.facts("e-01"); !equal(f["task.wake_until"], time.Time{}) || f["task.wake_when"] != "" {
		t.Fatalf("no wake: until %v, when %q", f["task.wake_until"], f["task.wake_when"])
	}
	until := monday.Add(48 * time.Hour)
	if err := e.db.Wakes().Put(context.Background(), wake.Wake{Task: "e-01", Until: until, When: `size(task.link_owes) != 0`}); err != nil {
		t.Fatal(err)
	}
	if f := e.facts("e-01"); !equal(f["task.wake_until"], until) || f["task.wake_when"] != `size(task.link_owes) != 0` {
		t.Fatalf("the wake: until %v, when %q", f["task.wake_until"], f["task.wake_when"])
	}
}

func TestTheOwedFactsFollowWhoOwesTheTask(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	friday := time.Date(2026, 2, 27, 9, 0, 0, 0, brt)
	theirs := e.item(chatA, "i1", items.Open, friday, items.Person{Key: "chat:U1"})
	e.task(waiting("e-01", chatA, monday, theirs))
	f := e.facts("e-01")
	if f["task.owed_by"] != "chat:U1" || !equal(f["task.owed_since"], friday) || !equal(f["task.check_on"], time.Date(2026, 3, 3, 9, 0, 0, 0, brt)) {
		t.Errorf("owed by %v since %v, check on %v; want chat:U1 since friday, check on tuesday", f["task.owed_by"], f["task.owed_since"], f["task.check_on"])
	}
	checked := waiting("e-02", chatB, monday)
	checked.SetProp(task.PropOwner, task.Value{Actors: []items.Person{{Key: "chat:U9"}}}, task.WriterPerson, monday)
	checked.SetProp(task.PropCheckOn, task.Value{Time: monday.Add(5 * time.Hour)}, task.WriterPerson, monday)
	e.task(checked)
	if f := e.facts("e-02"); f["task.owed_by"] != "chat:U9" || !equal(f["task.check_on"], monday.Add(5*time.Hour)) || !equal(f["task.owed_since"], monday) {
		t.Errorf("a task its owner owes: by %v, check on %v, since %v", f["task.owed_by"], f["task.check_on"], f["task.owed_since"])
	}
	if _, err := e.db.Gates(func() time.Time { return monday }).Open(context.Background(), gate.Gate{Task: "e-01", Unit: chatA, Kind: gate.KindDraft, Name: "draft", Subject: "1"}); err != nil {
		t.Fatal(err)
	}
	if f := e.facts("e-01"); f["task.owed_by"] != "me" || !equal(f["task.check_on"], time.Date(2026, 3, 3, 9, 0, 0, 0, brt)) {
		t.Errorf("with a gate open: owed by %v, check on %v; want me, the check date its items keep", f["task.owed_by"], f["task.check_on"])
	}
	mine := e.item(chatB, "i2", items.Open, friday, items.Person{Key: "chat:ME"})
	e.task(task.Task{ID: "e-03", State: "next", Category: task.CategoryReady, Title: "m", Units: []unit.Unit{chatB}, Items: []task.ItemRef{{Unit: chatB, Item: mine.ID}}})
	if f := e.facts("e-03"); f["task.owed_by"] != "me" || !equal(f["task.check_on"], time.Time{}) {
		t.Errorf("my task: by %v, check on %v", f["task.owed_by"], f["task.check_on"])
	}
}

func equal(a, b any) bool {
	if at, ok := a.(time.Time); ok {
		bt, ok := b.(time.Time)
		return ok && at.Equal(bt)
	}
	return a == b
}

func TestTaskDueIsTheNextCheckOrTheEndOfANudgesQuiet(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	soon := waiting("e-02", chatB, monday)
	soon.SetProp(task.PropCheckOn, task.Value{Time: monday.Add(5 * time.Hour)}, task.WriterPerson, monday)
	e.task(soon)
	nudged := waiting("e-03", chatA, monday.Add(-10*24*time.Hour))
	nudged.SetProp(task.PropLastNudge, task.Value{Time: monday.Add(-71 * time.Hour)}, task.WriterWorkflow, monday)
	e.task(nudged)
	list := func() time.Time {
		t.Helper()
		if _, err := e.p.List(context.Background(), unit.Task); err != nil {
			t.Fatal(err)
		}
		return e.p.Due()
	}
	if got, want := list(), monday.Add(time.Hour); !got.Equal(want) {
		t.Fatalf("due %v, want e-03's quiet end %v", got, want)
	}
	e.clock.now = monday.Add(time.Hour)
	if got, want := list(), monday.Add(5*time.Hour); !got.Equal(want) {
		t.Fatalf("due %v, want e-02's check %v", got, want)
	}
}

func TestATasksOwnerIsItsUnit(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	i1 := e.item(chatB, "i1", items.Open, monday)
	e.task(waiting("e-01", chatA, monday))
	e.task(task.Task{ID: "e-02", State: "next", Category: task.CategoryReady, Title: "a", Items: []task.ItemRef{{Unit: i1.Unit, Item: i1.ID}}})
	e.task(task.Task{ID: "e-03", State: "inbox", Category: task.CategoryInbox, Title: "c"})
	e.task(task.Task{ID: "e-04", State: "next", Category: task.CategoryReady, Title: "c", Units: []unit.Unit{chatB, chatA}})
	for id, want := range map[task.ID][]unit.Unit{"e-01": {chatA}, "e-02": {chatB}, "e-03": nil, "e-04": {chatB, chatA}, "e-99": nil} {
		got, err := e.p.Owners(context.Background(), task.UnitOf(id))
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("owners of %s: %v, %v; want %v", id, got, err, want)
		}
	}
}

func TestTaskSettledAtIsTheWorkflowsOwn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	e.task(waiting("e-01", chatA, monday))
	put := func(wf string, s draft.State, at time.Time) {
		t.Helper()
		d := draft.Draft{Unit: chatA, Task: "e-01", Kind: draft.Nudge, Purpose: "nudge", Delivery: draft.Message, Text: "x", Original: "x",
			Origin: draft.Origin{Kind: draft.OriginAuto, Workflow: wf, Rule: wf, Key: "e-01:chat:U1"},
			State:  s, CreatedAt: at, UpdatedAt: at}
		if s == draft.Dropped {
			d.Reason = draft.WithdrawMoot
		}
		if _, err := e.db.Drafts().Insert(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	put("nudge-a", draft.Sent, monday.Add(-3*time.Hour))
	put("nudge-a", draft.Dropped, monday.Add(-2*time.Hour))
	put("nudge-a", draft.Held, monday.Add(-time.Hour))
	put("nudge-b", draft.Sent, monday.Add(-30*time.Minute))
	f := e.facts("e-01")
	scoped, err := e.p.Scope(ctx, "nudge-a", task.UnitOf("e-01"), f)
	if err != nil {
		t.Fatal(err)
	}
	if got := scoped["task.settled_at"].(time.Time); !got.Equal(monday.Add(-2 * time.Hour)) {
		t.Errorf("nudge-a settled_at %v, want its withdrawn draft's", got)
	}
	if got := f["task.settled_at"].(time.Time); !got.IsZero() {
		t.Errorf("Scope wrote the unit's facts: %v", got)
	}
	if fresh, _ := e.p.Scope(ctx, "nudge-c", task.UnitOf("e-01"), f); !fresh["task.settled_at"].(time.Time).IsZero() {
		t.Errorf("a workflow with no drafts: %v", fresh["task.settled_at"])
	}
}

func TestATaskThatLeftItsListStillHasEventFacts(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.task(task.Task{ID: "e-05", State: "done", Category: task.CategoryDone, Title: "d", Units: []unit.Unit{chatA}})
	e.task(task.Task{ID: "e-04", State: "someday", Category: task.CategoryDeferred, Title: "m", Units: []unit.Unit{mailX}})
	f, err := e.p.EventFacts(context.Background(), task.UnitOf("e-05"))
	if err != nil || f["task.state"] != "done" || f["task.unit"] != chatA.String() {
		t.Fatalf("event facts of a done entry: %v %v", f, err)
	}
	for _, id := range []task.ID{"e-04", "e-99"} {
		if _, err := e.p.EventFacts(context.Background(), task.UnitOf(id)); err != automation.ErrUnitGone {
			t.Errorf("event facts of %s: %v, want ErrUnitGone", id, err)
		}
	}
}

var docY = unit.Unit{Provider: "doc", Key: "d-1"}

func TestTaskFactsSpanEveryUnitInOrder(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.item(chatA, "i1", items.Open, monday)
	owed := e.item(chatB, "i2", items.Open, monday, items.Person{Key: "chat:U1"})
	e.item(chatB, "i3", items.Resolved, monday)
	onDoc := e.item(docY, "i4", items.Open, monday)
	en := task.Task{ID: "e-01", State: "next", Category: task.CategoryReady, Title: "ship it", Units: []unit.Unit{chatA, chatB},
		Items: []task.ItemRef{{Unit: owed.Unit, Item: owed.ID}, {Unit: onDoc.Unit, Item: onDoc.ID}}}
	e.task(en)
	f := e.facts("e-01")
	if f["task.unit"] != chatA.String() || f["task.unit_provider"] != "chat" {
		t.Errorf("the first unit: %v %v", f["task.unit"], f["task.unit_provider"])
	}
	if got := f["task.units"].([]string); !slices.Equal(got, []string{chatA.String(), chatB.String(), docY.String()}) {
		t.Errorf("units %v, want the attached ones in order, then the one only an item names", got)
	}
	if got := f["task.unit_providers"].([]string); !slices.Equal(got, []string{"chat", "doc"}) {
		t.Errorf("unit_providers %v", got)
	}
	if f["task.items_open"] != 3 || f["task.items_resolved"] != 1 {
		t.Errorf("items open %v, resolved %v; want 3, 1 over every unit", f["task.items_open"], f["task.items_resolved"])
	}
}

func TestTaskUnitsEndedWhenEveryUnitEnded(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.task(task.Task{ID: "e-01", State: "next", Category: task.CategoryReady, Title: "none"})
	e.task(task.Task{ID: "e-02", State: "next", Category: task.CategoryReady, Title: "two", Units: []unit.Unit{chatA, chatB}})
	if f := e.facts("e-01"); f["task.link_ended"] != false {
		t.Error("an entry with no unit has its units ended")
	}
	e.ended[chatA] = true
	if f := e.facts("e-02"); f["task.link_ended"] != false {
		t.Error("an entry with a unit still going has its units ended")
	}
	e.ended[chatB] = true
	if f := e.facts("e-02"); f["task.link_ended"] != true {
		t.Error("an entry whose every unit ended has not")
	}
}

func TestTaskRepoIsTheFirstRepositoryOfItsUnits(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.task(task.Task{ID: "e-01", State: "next", Category: task.CategoryReady, Title: "none"})
	e.task(task.Task{ID: "e-02", State: "next", Category: task.CategoryReady, Title: "two", Units: []unit.Unit{chatA, chatB}})
	e.repos[chatB] = "acme/api"
	if f := e.facts("e-01"); f["task.repo"] != "" {
		t.Errorf("a task with no unit is of %q", f["task.repo"])
	}
	if f := e.facts("e-02"); f["task.repo"] != "acme/api" {
		t.Errorf("task.repo %q, want its second unit's, the first that has one", f["task.repo"])
	}
	e.repos[chatA] = "acme/web"
	if f := e.facts("e-02"); f["task.repo"] != "acme/web" {
		t.Errorf("task.repo %q, want its first unit's", f["task.repo"])
	}
}

func TestTaskTurnsCountsTheTurnsOnTheTaskItself(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.task(task.Task{ID: "e-01", State: "next", Category: task.CategoryReady, Title: "tracked", Units: []unit.Unit{chatA}})
	e.turns[chatA] = 3
	if f := e.facts("e-01"); f["task.turns"] != 0 {
		t.Errorf("task.turns %v, want the turns on its unit left out", f["task.turns"])
	}
	e.turns[task.UnitOf("e-01")] = 2
	if f := e.facts("e-01"); f["task.turns"] != 2 {
		t.Errorf("task.turns %v, want 2", f["task.turns"])
	}
}

func TestTaskCapturedByUser(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.task(task.Task{ID: "e-01", State: "inbox", Category: task.CategoryInbox, Title: "mine"})
	e.task(task.Task{ID: "e-02", State: "inbox", Category: task.CategoryInbox, Title: "derived", Origin: task.OriginDerived, Units: []unit.Unit{chatA}})
	e.task(task.Task{ID: "e-03", State: "inbox", Category: task.CategoryInbox, Title: "a message", Origin: task.OriginMessage, Units: []unit.Unit{chatA}})
	for id, want := range map[task.ID]bool{"e-01": true, "e-02": false, "e-03": false} {
		if got := e.facts(id)["task.captured_by_user"]; got != want {
			t.Errorf("%s: captured_by_user %v, want %v", id, got, want)
		}
	}
}

func TestGatesOpenCountsTheTasksOpenGates(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.task(task.Task{ID: "t-00000000000000a1", State: "inbox", Category: task.CategoryInbox, Title: "a"})
	e.task(task.Task{ID: "t-00000000000000b2", State: "inbox", Category: task.CategoryInbox, Title: "b"})
	gs := e.db.Gates(func() time.Time { return e.clock.now })
	open := func(id task.ID, subject string) gate.Gate {
		g, err := gs.Open(ctx, gate.Gate{Task: id, Unit: task.UnitOf(id), Kind: gate.KindQuestion, Name: "question", Subject: subject, Intents: []string{"answer", "dismiss"}})
		if err != nil {
			t.Fatal(err)
		}
		return g
	}
	open("t-00000000000000a1", "1")
	closed := open("t-00000000000000a1", "2")
	open("t-00000000000000a1", "3")
	open("t-00000000000000b2", "4")
	if _, err := gs.Resolve(ctx, closed.ID, gates.Resolution{Status: gate.Dismissed, Intent: "dismiss", By: task.ActorPerson}); err != nil {
		t.Fatal(err)
	}
	if got := e.facts("t-00000000000000a1")["task.gates_open"]; got != 2 {
		t.Fatalf("task.gates_open = %v, want 2: its open gates, not the closed one nor another task's", got)
	}
}
