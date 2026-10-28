package tasks_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var (
	brt, _ = time.LoadLocation("America/Sao_Paulo")
	t0     = time.Date(2026, 10, 20, 10, 0, 0, 0, brt)
	me     = task.Me{Keys: []string{"github:me"}, Codecs: unit.NewRegistry(foldCodec{unit.GitHub})}
	meP    = items.Person{Key: "github:me"}
	ana    = items.Person{Key: "github:ana", Name: "Ana"}
	unitU  = unit.Unit{Provider: unit.GitHub, Key: "acme/api#1"}
	unitV  = unit.Unit{Provider: unit.GitHub, Key: "acme/api#2"}
)

type clock struct{ t time.Time }

func (c *clock) Now() time.Time { return c.t }

type fakeItems struct {
	byUnit map[unit.Unit][]items.Item
	reads  map[unit.Unit]int
}

func (f *fakeItems) Items(ctx context.Context, u unit.Unit) ([]items.Item, error) {
	f.reads[u]++
	return f.byUnit[u], nil
}

type fakeFacts struct {
	facts map[unit.Unit]task.UnitFacts
	off   map[unit.ProviderID]bool
}

func (f *fakeFacts) UnitFacts(ctx context.Context, u unit.Unit) (task.UnitFacts, bool, error) {
	if f.off[u.Provider] {
		return task.UnitFacts{}, false, nil
	}
	fs, ok := f.facts[u]
	if !ok {
		fs = task.UnitFacts{Unit: u}
	}
	return fs, true, nil
}

type rig struct {
	svc   *coretasks.Service
	store *taskstest.Store
	items *fakeItems
	facts *fakeFacts
	clock *clock
}

func newRig() *rig {
	r := &rig{store: taskstest.New(), items: &fakeItems{byUnit: map[unit.Unit][]items.Item{}, reads: map[unit.Unit]int{}},
		facts: &fakeFacts{facts: map[unit.Unit]task.UnitFacts{}, off: map[unit.ProviderID]bool{}}, clock: &clock{t0}}
	r.svc = coretasks.New(coretasks.Deps{Store: r.store, Items: r.items, Facts: r.facts, Me: me, Clock: r.clock, StaleWaitingDays: 2})
	return r
}

func owed(u unit.Unit, id string) items.Item {
	return items.Item{ID: id, Unit: u, Summary: "do " + id, State: "open", Requester: ana, WaitingOn: []items.Person{meP}, CreatedAt: t0}
}

func (r *rig) put(t *testing.T, e task.Entry) task.Entry {
	t.Helper()
	if e.Created.IsZero() {
		e.Created, e.Updated = t0, t0
	}
	if err := r.store.Apply(context.Background(), []task.Change{{Op: task.OpCreate, Entry: e}}); err != nil {
		t.Fatal(err)
	}
	return e
}

func (r *rig) entry(t *testing.T, id task.ID) task.Entry {
	t.Helper()
	e, err := r.store.Entry(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

var allStates = []task.State{task.Inbox, task.Next, task.Waiting, task.Scheduled, task.Someday, task.Reference, task.Done, task.Trash}

func TestVerbTransitions(t *testing.T) {
	const (
		I, N, W, S, Y, R, D, T = task.Inbox, task.Next, task.Waiting, task.Scheduled, task.Someday, task.Reference, task.Done, task.Trash
		keep                   = task.State("keep")
		no                     = task.State("")
	)
	type row map[task.State]task.State
	table := map[task.Verb]row{
		task.VerbNext:      {I: N, N: no, W: N, S: N, Y: N, R: N, D: no, T: no},
		task.VerbWaiting:   {I: W, N: W, W: no, S: W, Y: W, R: no, D: no, T: no},
		task.VerbSchedule:  {I: S, N: S, W: S, S: S, Y: S, R: no, D: no, T: no},
		task.VerbSomeday:   {I: Y, N: Y, W: Y, S: Y, Y: no, R: no, D: no, T: no},
		task.VerbReference: {I: R, N: R, W: R, S: R, Y: R, R: no, D: R, T: no},
		task.VerbDone:      {I: D, N: D, W: D, S: D, Y: D, R: D, D: no, T: no},
		task.VerbDismiss:   {I: T, N: T, W: T, S: T, Y: T, R: T, D: T, T: no},
		task.VerbFocus:     {I: keep, N: keep, W: keep, S: keep, Y: keep, R: keep, D: no, T: no},
		task.VerbContext:   {I: keep, N: keep, W: keep, S: keep, Y: keep, R: keep, D: no, T: no},
		task.VerbArea:      {I: keep, N: keep, W: keep, S: keep, Y: keep, R: keep, D: no, T: no},
		task.VerbSeen:      {I: keep, N: keep, W: keep, S: keep, Y: keep, R: keep, D: keep, T: keep},
		task.VerbApprove:   {},
		task.VerbDelegate:  {},
		task.VerbAnswer:    {},
		task.VerbEdit:      {},
		task.VerbRetry:     {},
	}
	args := map[task.Verb]map[string]string{task.VerbSchedule: {"until": "2026-10-30"}, task.VerbContext: {"set": "@a"}, task.VerbArea: {"set": "OSS"}}
	ctx := context.Background()
	for verb, want := range table {
		for _, from := range allStates {
			r := newRig()
			e := r.put(t, task.Entry{ID: "e-x", Kind: task.KindCapture, State: from, Origin: task.OriginUser,
				Snooze: task.Snooze{Until: t0.Add(time.Hour), Return: task.Next}})
			res, err := r.svc.Do(ctx, e.ID, coretasks.Request{Verb: verb, Args: args[verb]})
			to := want[from]
			if to == no {
				if !errors.Is(err, coretasks.ErrVerb) || !strings.Contains(err.Error(), string(verb)) || !strings.Contains(err.Error(), string(from)) {
					t.Errorf("%s on %s: %v, want ErrVerb naming both", verb, from, err)
				}
				continue
			}
			if to == keep {
				to = from
			}
			if err != nil {
				t.Errorf("%s on %s: %v", verb, from, err)
				continue
			}
			if res.Entry.State != to || r.entry(t, e.ID).State != to {
				t.Errorf("%s on %s: %s, want %s", verb, from, res.Entry.State, to)
			}
		}
	}
	r := newRig()
	r.put(t, task.Entry{ID: "e-x", State: task.Inbox})
	if _, err := r.svc.Do(ctx, "e-x", coretasks.Request{Verb: "fly"}); !errors.Is(err, coretasks.ErrArgs) {
		t.Errorf("an unknown verb: %v", err)
	}
	if _, err := r.svc.Do(ctx, "e-x", coretasks.Request{Verb: task.VerbSchedule}); !errors.Is(err, coretasks.ErrArgs) {
		t.Errorf("schedule without a date: %v", err)
	}
	if _, err := r.svc.Do(ctx, "e-missing", coretasks.Request{Verb: task.VerbNext}); !errors.Is(err, coretasks.ErrNoEntry) {
		t.Errorf("a missing entry: %v", err)
	}
}

func TestScheduleKeepsWhereItReturns(t *testing.T) {
	r := newRig()
	r.put(t, task.Entry{ID: "e-x", State: task.Next})
	res, err := r.svc.Do(context.Background(), "e-x", coretasks.Request{Verb: task.VerbSchedule, Args: map[string]string{"until": "2026-10-30", "on_activity": "true"}})
	if err != nil {
		t.Fatal(err)
	}
	want := task.Snooze{Until: time.Date(2026, 10, 30, 18, 0, 0, 0, brt), OnActivity: true, Return: task.Next}
	if s := res.Entry.Snooze; !s.Until.Equal(want.Until) || s.OnActivity != want.OnActivity || s.Return != want.Return {
		t.Fatalf("snooze %+v", s)
	}
}

func TestFocusNeverMovesTheEntry(t *testing.T) {
	r := newRig()
	for _, s := range []task.State{task.Inbox, task.Next, task.Waiting, task.Scheduled, task.Someday, task.Reference} {
		id := task.ID("e-" + string(s))
		r.put(t, task.Entry{ID: id, State: s})
		for _, on := range []bool{true, false} {
			res, err := r.svc.Do(context.Background(), id, coretasks.Request{Verb: task.VerbFocus})
			if err != nil || res.Entry.State != s || res.Entry.Focus.On != on || res.Entry.Focus.Auto != "" {
				t.Fatalf("%s: %+v %v", s, res.Entry, err)
			}
		}
	}
}

func TestSeenClearsTheMark(t *testing.T) {
	r := newRig()
	r.put(t, task.Entry{ID: "e-x", State: task.Inbox, Changed: true})
	r.clock.t = t0.Add(time.Hour)
	res, err := r.svc.Do(context.Background(), "e-x", coretasks.Request{Verb: task.VerbSeen})
	if err != nil || res.Entry.Changed || !res.Entry.SeenAt.Equal(r.clock.t) || res.Entry.State != task.Inbox {
		t.Fatalf("%+v %v", res.Entry, err)
	}
}

func TestSyncTouchesOnlyNotedUnits(t *testing.T) {
	r := newRig()
	r.items.byUnit[unitU] = []items.Item{owed(unitU, "a")}
	r.items.byUnit[unitV] = []items.Item{owed(unitV, "b")}
	if err := r.svc.Sync(context.Background(), []unit.Unit{unitU, unitU}); err != nil {
		t.Fatal(err)
	}
	if r.items.reads[unitV] != 0 || r.items.reads[unitU] != 1 {
		t.Fatalf("reads %v", r.items.reads)
	}
	u, _ := r.store.EntriesOf(context.Background(), unitU)
	v, _ := r.store.EntriesOf(context.Background(), unitV)
	if len(u) != 1 || len(v) != 0 {
		t.Fatalf("entries of U %v, of V %v", u, v)
	}
	if r.store.Applies != 1 {
		t.Fatalf("%d applies", r.store.Applies)
	}
	if err := r.svc.Sync(context.Background(), []unit.Unit{unitU}); err != nil || r.store.Notes != 1 {
		t.Fatalf("a second sync noted: %d %v", r.store.Notes, err)
	}
}

func TestSyncRunsFocusAndWakeInOneApply(t *testing.T) {
	r := newRig()
	r.put(t, task.Entry{ID: "e-due", State: task.Next, Deadline: t0.Add(-time.Minute)})
	r.put(t, task.Entry{ID: "e-wake", State: task.Scheduled, Snooze: task.Snooze{Until: t0, Return: task.Someday}})
	r.items.byUnit[unitU] = []items.Item{owed(unitU, "a")}
	r.store.Applies, r.store.Notes = 0, 0
	if err := r.svc.Sync(context.Background(), []unit.Unit{unitU}); err != nil {
		t.Fatal(err)
	}
	if r.store.Applies != 1 {
		t.Fatalf("%d applies", r.store.Applies)
	}
	if e := r.entry(t, "e-due"); !e.Focus.On || e.Focus.Auto != task.FocusDeadline {
		t.Fatalf("due %+v", e.Focus)
	}
	if e := r.entry(t, "e-wake"); e.State != task.Someday {
		t.Fatalf("wake %+v", e)
	}
}

func TestDisabledPluginEntriesStay(t *testing.T) {
	r := newRig()
	r.items.byUnit[unitU] = []items.Item{owed(unitU, "a")}
	if err := r.svc.Sync(context.Background(), []unit.Unit{unitU}); err != nil {
		t.Fatal(err)
	}
	es, _ := r.store.EntriesOf(context.Background(), unitU)
	e := es[0]
	e.State, e.Snooze, e.Deadline = task.Scheduled, task.Snooze{Until: t0, Return: task.Next}, t0
	r.put(t, e)
	r.facts.off[unit.GitHub] = true
	r.items.byUnit[unitU][0].State = "resolved"
	r.clock.t = t0.Add(time.Hour)
	if err := r.svc.Sync(context.Background(), []unit.Unit{unitU}); err != nil {
		t.Fatal(err)
	}
	if got := r.entry(t, e.ID); !got.Same(e) {
		t.Fatalf("the entry changed: %+v", got)
	}
	if _, err := r.svc.Do(context.Background(), e.ID, coretasks.Request{Verb: task.VerbDone}); !errors.Is(err, coretasks.ErrPluginDisabled) {
		t.Fatalf("a verb on a disabled plugin's entry: %v", err)
	}
	inbox := r.put(t, task.Entry{ID: "e-msg", Kind: task.KindMessage, State: task.Inbox, Units: []unit.Unit{unitU}, Origin: task.OriginMessage})
	if got, err := r.svc.Entry(context.Background(), inbox.ID); err != nil || got.ID != inbox.ID {
		t.Fatalf("the disabled plugin's inbox entry is not readable: %v %v", got, err)
	}
	if got, err := r.svc.Entry(context.Background(), e.ID); err != nil || !got.Same(e) {
		t.Fatalf("the disabled plugin's entry is not readable: %+v %v", got, err)
	}
	if got, err := r.svc.Entries(context.Background(), task.Query{View: task.ViewOpen}); err == nil || len(got) != 0 {
		t.Fatalf("the open view is served: %v", err)
	}
}

func TestOpenAndCloseOwnedEntries(t *testing.T) {
	r := newRig()
	ctx := context.Background()
	e, err := r.svc.Open(ctx, coretasks.OpenRequest{Kind: task.KindMessage, Title: "run the round?", Unit: &unitU})
	if err != nil {
		t.Fatal(err)
	}
	other, _ := r.svc.Open(ctx, coretasks.OpenRequest{Kind: task.KindMessage, Title: "again"})
	if e.ID == other.ID || len(e.ID) != 18 || e.State != task.Inbox || e.Origin != task.OriginMessage || !slices.Equal(e.Units, []unit.Unit{unitU}) {
		t.Fatalf("opened %+v", e)
	}
	if err := r.svc.Close(ctx, e.ID, task.Done); err != nil || r.entry(t, e.ID).State != task.Done {
		t.Fatalf("close: %v", err)
	}
	if err := r.svc.Close(ctx, e.ID, task.Next); !errors.Is(err, coretasks.ErrArgs) {
		t.Fatalf("close to next: %v", err)
	}
	r.items.byUnit[unitU] = []items.Item{owed(unitU, "a")}
	if err := r.svc.Sync(ctx, []unit.Unit{unitU}); err != nil {
		t.Fatal(err)
	}
	derived := task.DerivedID(task.KindAction, unitU, "a")
	if err := r.svc.Close(ctx, derived, task.Done); !errors.Is(err, coretasks.ErrVerb) {
		t.Fatalf("close on a derived entry: %v", err)
	}
	if c, err := r.svc.Capture(ctx, "call Ana", "", nil); err != nil || c.Kind != task.KindCapture || c.State != task.Inbox {
		t.Fatalf("capture %+v %v", c, err)
	}
}

type outbox struct{ drafts []task.Entry }

func (o outbox) Entries(ctx context.Context, q task.Query) ([]task.Entry, error) {
	return o.drafts, nil
}

func (o outbox) Entry(ctx context.Context, id task.ID) (task.Entry, bool, error) {
	for _, d := range o.drafts {
		if d.ID == id {
			return d, true, nil
		}
	}
	return task.Entry{}, false, nil
}

func (o outbox) Count(ctx context.Context, area string) (int, error) { return len(o.drafts), nil }

func TestRegisteredViewSource(t *testing.T) {
	ctx := context.Background()
	r := newRig()
	r.put(t, task.Entry{ID: "e-in", State: task.Inbox})
	if es, err := r.svc.Entries(ctx, task.Query{View: task.ViewOutbox}); err != nil || len(es) != 0 {
		t.Fatalf("outbox without a source: %v %v", es, err)
	}
	if c, err := r.svc.Counts(ctx, ""); err != nil || c.Views[task.ViewOutbox] != 0 || c.Badge != 1 {
		t.Fatalf("counts without a source: %+v %v", c, err)
	}
	if _, err := r.svc.Entry(ctx, "draft:7"); !errors.Is(err, coretasks.ErrNoEntry) {
		t.Fatalf("a draft without a source: %v", err)
	}
	src := outbox{drafts: []task.Entry{{ID: "draft:7", Kind: task.KindDraft, Origin: task.OriginDraft}, {ID: "draft:8", Kind: task.KindDraft}}}
	r.svc.RegisterView(task.ViewOutbox, "draft:", src)
	if es, err := r.svc.Entries(ctx, task.Query{View: task.ViewOutbox}); err != nil || len(es) != 2 {
		t.Fatalf("outbox: %v %v", es, err)
	}
	if e, err := r.svc.Entry(ctx, "draft:7"); err != nil || e.Kind != task.KindDraft {
		t.Fatalf("draft:7 %+v %v", e, err)
	}
	if _, err := r.svc.Entry(ctx, "draft:9"); !errors.Is(err, coretasks.ErrNoEntry) {
		t.Fatalf("draft:9: %v", err)
	}
	if c, err := r.svc.Counts(ctx, ""); err != nil || c.Views[task.ViewOutbox] != 2 || c.Badge != 3 {
		t.Fatalf("counts: %+v %v", c, err)
	}
	var ran []string
	r.svc.Register(task.KindDraft, task.VerbApprove, func(ctx context.Context, e task.Entry, req coretasks.Request) (coretasks.Result, error) {
		ran = append(ran, fmt.Sprint(e.ID, " ", req.Verb))
		return coretasks.Result{Entry: e}, nil
	})
	if _, err := r.svc.Do(ctx, "draft:7", coretasks.Request{Verb: task.VerbApprove}); err != nil || !slices.Equal(ran, []string{"draft:7 approve"}) {
		t.Fatalf("approve: %v %v", ran, err)
	}
	if _, err := r.svc.Do(ctx, "draft:7", coretasks.Request{Verb: task.VerbNext}); !errors.Is(err, coretasks.ErrVerb) {
		t.Fatalf("next on a draft: %v", err)
	}
}

func TestUserScheduleSurvivesMute(t *testing.T) {
	ctx := context.Background()
	r := newRig()
	r.items.byUnit[unitU] = []items.Item{owed(unitU, "a")}
	muteEnd := t0.Add(72 * time.Hour)
	r.facts.facts[unitU] = task.UnitFacts{Unit: unitU, Mute: &task.Mute{Until: muteEnd}}
	if err := r.svc.Sync(ctx, []unit.Unit{unitU}); err != nil {
		t.Fatal(err)
	}
	id := task.DerivedID(task.KindAction, unitU, "a")
	if e := r.entry(t, id); e.State != task.Scheduled || !e.Snooze.Mute {
		t.Fatalf("muted: %+v", e)
	}
	mine := time.Date(2026, 12, 1, 18, 0, 0, 0, brt)
	if _, err := r.svc.Do(ctx, id, coretasks.Request{Verb: task.VerbSchedule, Args: map[string]string{"until": "2026-12-01"}}); err != nil {
		t.Fatal(err)
	}
	check := func(when string) {
		t.Helper()
		e := r.entry(t, id)
		if e.State != task.Scheduled || !e.Snooze.Until.Equal(mine) || e.Snooze.Mute {
			t.Fatalf("%s: %+v", when, e)
		}
	}
	r.clock.t = t0.Add(time.Hour)
	if err := r.svc.Sync(ctx, []unit.Unit{unitU}); err != nil {
		t.Fatal(err)
	}
	check("still muted")
	r.facts.facts[unitU] = task.UnitFacts{Unit: unitU}
	r.clock.t = muteEnd.Add(time.Hour)
	if err := r.svc.Sync(ctx, []unit.Unit{unitU}); err != nil {
		t.Fatal(err)
	}
	check("unmuted")
}

func TestCloseRefusesADisabledPlugin(t *testing.T) {
	r := newRig()
	ctx := context.Background()
	e, err := r.svc.Open(ctx, coretasks.OpenRequest{Kind: task.KindMessage, Title: "run the round?", Unit: &unitU})
	if err != nil {
		t.Fatal(err)
	}
	r.facts.off[unit.GitHub] = true
	if err := r.svc.Close(ctx, e.ID, task.Done); !errors.Is(err, coretasks.ErrPluginDisabled) {
		t.Fatalf("close on a disabled plugin's entry: %v", err)
	}
	if got := r.entry(t, e.ID); got.State != task.Inbox {
		t.Fatalf("the entry moved to %s", got.State)
	}
}

func TestRetitleUpdatesAnOpenedEntry(t *testing.T) {
	r := newRig()
	ctx := context.Background()
	e, err := r.svc.Open(ctx, coretasks.OpenRequest{Kind: task.KindMessage, Title: "run the round?", Unit: &unitU})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := r.store.EntriesSince(ctx, task.Query{View: task.ViewInbox}, coretasks.Since{})
	r.clock.t = t0.Add(time.Minute)
	if err := r.svc.Retitle(ctx, e.ID, "hold the round?"); err != nil {
		t.Fatal(err)
	}
	if got := r.entry(t, e.ID); got.Title != "hold the round?" || !got.Updated.Equal(t0.Add(time.Minute)) || got.State != task.Inbox {
		t.Fatalf("entry %+v", got)
	}
	page, _ := r.store.EntriesSince(ctx, task.Query{View: task.ViewInbox}, coretasks.Since{Epoch: before.Epoch, Rev: before.Rev})
	if page.Full || len(page.Entries) != 1 {
		t.Fatalf("page %+v", page)
	}
	if err := r.svc.Retitle(ctx, e.ID, "hold the round?"); err != nil {
		t.Fatal(err)
	}
	r.items.byUnit[unitU] = []items.Item{owed(unitU, "a")}
	if err := r.svc.Sync(ctx, []unit.Unit{unitU}); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Retitle(ctx, task.DerivedID(task.KindAction, unitU, "a"), "x"); !errors.Is(err, coretasks.ErrVerb) {
		t.Fatalf("retitle of a derived entry: %v", err)
	}
}

// foldCodec compares its provider's keys without case, as GitHub's does.
type foldCodec struct{ id unit.ProviderID }

func (c foldCodec) Provider() unit.ProviderID { return c.id }
func (foldCodec) Valid(string) error          { return nil }
func (foldCodec) Equal(a, b string) bool      { return strings.EqualFold(a, b) }
