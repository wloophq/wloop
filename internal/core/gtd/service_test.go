package gtd_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/core/gtd/gtdtest"
	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var (
	brt, _ = time.LoadLocation("America/Sao_Paulo")
	t0     = time.Date(2026, 10, 20, 10, 0, 0, 0, brt)
	me     = gtd.Me{Keys: []string{"github:me"}}
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
	facts map[unit.Unit]gtd.UnitFacts
	off   map[unit.ProviderID]bool
}

func (f *fakeFacts) UnitFacts(ctx context.Context, u unit.Unit) (gtd.UnitFacts, bool, error) {
	if f.off[u.Provider] {
		return gtd.UnitFacts{}, false, nil
	}
	fs, ok := f.facts[u]
	if !ok {
		fs = gtd.UnitFacts{Unit: u}
	}
	return fs, true, nil
}

type rig struct {
	svc   *coregtd.Service
	store *gtdtest.Store
	items *fakeItems
	facts *fakeFacts
	clock *clock
}

func newRig() *rig {
	r := &rig{store: gtdtest.New(), items: &fakeItems{byUnit: map[unit.Unit][]items.Item{}, reads: map[unit.Unit]int{}},
		facts: &fakeFacts{facts: map[unit.Unit]gtd.UnitFacts{}, off: map[unit.ProviderID]bool{}}, clock: &clock{t0}}
	r.svc = coregtd.New(coregtd.Deps{Store: r.store, Items: r.items, Facts: r.facts, Me: me, Clock: r.clock, StaleWaitingDays: 2})
	return r
}

func owed(u unit.Unit, id string) items.Item {
	return items.Item{ID: id, Unit: u, Summary: "do " + id, State: "open", Requester: ana, WaitingOn: []items.Person{meP}, CreatedAt: t0}
}

func (r *rig) put(t *testing.T, e gtd.Entry) gtd.Entry {
	t.Helper()
	if e.Created.IsZero() {
		e.Created, e.Updated = t0, t0
	}
	if err := r.store.Apply(context.Background(), []gtd.Change{{Op: gtd.OpCreate, Entry: e}}); err != nil {
		t.Fatal(err)
	}
	return e
}

func (r *rig) entry(t *testing.T, id gtd.ID) gtd.Entry {
	t.Helper()
	e, err := r.store.Entry(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

var allStates = []gtd.State{gtd.Inbox, gtd.Next, gtd.Waiting, gtd.Scheduled, gtd.Someday, gtd.Reference, gtd.Done, gtd.Trash}

func TestVerbTransitions(t *testing.T) {
	const (
		I, N, W, S, Y, R, D, T = gtd.Inbox, gtd.Next, gtd.Waiting, gtd.Scheduled, gtd.Someday, gtd.Reference, gtd.Done, gtd.Trash
		keep                   = gtd.State("keep")
		no                     = gtd.State("")
	)
	type row map[gtd.State]gtd.State
	table := map[gtd.Verb]row{
		gtd.VerbNext:      {I: N, N: no, W: N, S: N, Y: N, R: N, D: no, T: no},
		gtd.VerbWaiting:   {I: W, N: W, W: no, S: W, Y: W, R: no, D: no, T: no},
		gtd.VerbSchedule:  {I: S, N: S, W: S, S: S, Y: S, R: no, D: no, T: no},
		gtd.VerbSomeday:   {I: Y, N: Y, W: Y, S: Y, Y: no, R: no, D: no, T: no},
		gtd.VerbReference: {I: R, N: R, W: R, S: R, Y: R, R: no, D: R, T: no},
		gtd.VerbDone:      {I: D, N: D, W: D, S: D, Y: D, R: D, D: no, T: no},
		gtd.VerbDismiss:   {I: T, N: T, W: T, S: T, Y: T, R: T, D: T, T: no},
		gtd.VerbFocus:     {I: keep, N: keep, W: keep, S: keep, Y: keep, R: keep, D: no, T: no},
		gtd.VerbContext:   {I: keep, N: keep, W: keep, S: keep, Y: keep, R: keep, D: no, T: no},
		gtd.VerbArea:      {I: keep, N: keep, W: keep, S: keep, Y: keep, R: keep, D: no, T: no},
		gtd.VerbSeen:      {I: keep, N: keep, W: keep, S: keep, Y: keep, R: keep, D: keep, T: keep},
		gtd.VerbApprove:   {},
		gtd.VerbDelegate:  {},
		gtd.VerbAnswer:    {},
		gtd.VerbEdit:      {},
		gtd.VerbRetry:     {},
	}
	args := map[gtd.Verb]map[string]string{gtd.VerbSchedule: {"until": "2026-10-30"}, gtd.VerbContext: {"set": "@a"}, gtd.VerbArea: {"set": "OSS"}}
	ctx := context.Background()
	for verb, want := range table {
		for _, from := range allStates {
			r := newRig()
			e := r.put(t, gtd.Entry{ID: "e-x", Kind: gtd.KindCapture, State: from, Origin: gtd.OriginUser,
				Snooze: gtd.Snooze{Until: t0.Add(time.Hour), Return: gtd.Next}})
			res, err := r.svc.Do(ctx, e.ID, coregtd.Request{Verb: verb, Args: args[verb]})
			to := want[from]
			if to == no {
				if !errors.Is(err, coregtd.ErrVerb) || !strings.Contains(err.Error(), string(verb)) || !strings.Contains(err.Error(), string(from)) {
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
	r.put(t, gtd.Entry{ID: "e-x", State: gtd.Inbox})
	if _, err := r.svc.Do(ctx, "e-x", coregtd.Request{Verb: "fly"}); !errors.Is(err, coregtd.ErrArgs) {
		t.Errorf("an unknown verb: %v", err)
	}
	if _, err := r.svc.Do(ctx, "e-x", coregtd.Request{Verb: gtd.VerbSchedule}); !errors.Is(err, coregtd.ErrArgs) {
		t.Errorf("schedule without a date: %v", err)
	}
	if _, err := r.svc.Do(ctx, "e-missing", coregtd.Request{Verb: gtd.VerbNext}); !errors.Is(err, coregtd.ErrNoEntry) {
		t.Errorf("a missing entry: %v", err)
	}
}

func TestScheduleKeepsWhereItReturns(t *testing.T) {
	r := newRig()
	r.put(t, gtd.Entry{ID: "e-x", State: gtd.Next})
	res, err := r.svc.Do(context.Background(), "e-x", coregtd.Request{Verb: gtd.VerbSchedule, Args: map[string]string{"until": "2026-10-30", "on_activity": "true"}})
	if err != nil {
		t.Fatal(err)
	}
	want := gtd.Snooze{Until: time.Date(2026, 10, 30, 18, 0, 0, 0, brt), OnActivity: true, Return: gtd.Next}
	if s := res.Entry.Snooze; !s.Until.Equal(want.Until) || s.OnActivity != want.OnActivity || s.Return != want.Return {
		t.Fatalf("snooze %+v", s)
	}
}

func TestFocusNeverMovesTheEntry(t *testing.T) {
	r := newRig()
	for _, s := range []gtd.State{gtd.Inbox, gtd.Next, gtd.Waiting, gtd.Scheduled, gtd.Someday, gtd.Reference} {
		id := gtd.ID("e-" + string(s))
		r.put(t, gtd.Entry{ID: id, State: s})
		for _, on := range []bool{true, false} {
			res, err := r.svc.Do(context.Background(), id, coregtd.Request{Verb: gtd.VerbFocus})
			if err != nil || res.Entry.State != s || res.Entry.Focus.On != on || res.Entry.Focus.Auto != "" {
				t.Fatalf("%s: %+v %v", s, res.Entry, err)
			}
		}
	}
}

func TestSeenClearsTheMark(t *testing.T) {
	r := newRig()
	r.put(t, gtd.Entry{ID: "e-x", State: gtd.Inbox, Changed: true})
	r.clock.t = t0.Add(time.Hour)
	res, err := r.svc.Do(context.Background(), "e-x", coregtd.Request{Verb: gtd.VerbSeen})
	if err != nil || res.Entry.Changed || !res.Entry.SeenAt.Equal(r.clock.t) || res.Entry.State != gtd.Inbox {
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
	r.put(t, gtd.Entry{ID: "e-due", State: gtd.Next, Deadline: t0.Add(-time.Minute)})
	r.put(t, gtd.Entry{ID: "e-wake", State: gtd.Scheduled, Snooze: gtd.Snooze{Until: t0, Return: gtd.Someday}})
	r.items.byUnit[unitU] = []items.Item{owed(unitU, "a")}
	r.store.Applies, r.store.Notes = 0, 0
	if err := r.svc.Sync(context.Background(), []unit.Unit{unitU}); err != nil {
		t.Fatal(err)
	}
	if r.store.Applies != 1 {
		t.Fatalf("%d applies", r.store.Applies)
	}
	if e := r.entry(t, "e-due"); !e.Focus.On || e.Focus.Auto != gtd.FocusDeadline {
		t.Fatalf("due %+v", e.Focus)
	}
	if e := r.entry(t, "e-wake"); e.State != gtd.Someday {
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
	e.State, e.Snooze, e.Deadline = gtd.Scheduled, gtd.Snooze{Until: t0, Return: gtd.Next}, t0
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
	if _, err := r.svc.Do(context.Background(), e.ID, coregtd.Request{Verb: gtd.VerbDone}); !errors.Is(err, coregtd.ErrPluginDisabled) {
		t.Fatalf("a verb on a disabled plugin's entry: %v", err)
	}
	inbox := r.put(t, gtd.Entry{ID: "e-msg", Kind: gtd.KindMessage, State: gtd.Inbox, Units: []unit.Unit{unitU}, Origin: gtd.OriginMessage})
	if got, err := r.svc.Entry(context.Background(), inbox.ID); err != nil || got.ID != inbox.ID {
		t.Fatalf("the disabled plugin's inbox entry is not readable: %v %v", got, err)
	}
	if got, err := r.svc.Entry(context.Background(), e.ID); err != nil || !got.Same(e) {
		t.Fatalf("the disabled plugin's entry is not readable: %+v %v", got, err)
	}
	if got, err := r.svc.Entries(context.Background(), gtd.Query{View: gtd.ViewOpen}); err == nil || len(got) != 0 {
		t.Fatalf("the open view is served: %v", err)
	}
}

func TestOpenAndCloseOwnedEntries(t *testing.T) {
	r := newRig()
	ctx := context.Background()
	e, err := r.svc.Open(ctx, coregtd.OpenRequest{Kind: gtd.KindMessage, Title: "run the round?", Unit: &unitU})
	if err != nil {
		t.Fatal(err)
	}
	other, _ := r.svc.Open(ctx, coregtd.OpenRequest{Kind: gtd.KindMessage, Title: "again"})
	if e.ID == other.ID || len(e.ID) != 18 || e.State != gtd.Inbox || e.Origin != gtd.OriginMessage || !slices.Equal(e.Units, []unit.Unit{unitU}) {
		t.Fatalf("opened %+v", e)
	}
	if err := r.svc.Close(ctx, e.ID, gtd.Done); err != nil || r.entry(t, e.ID).State != gtd.Done {
		t.Fatalf("close: %v", err)
	}
	if err := r.svc.Close(ctx, e.ID, gtd.Next); !errors.Is(err, coregtd.ErrArgs) {
		t.Fatalf("close to next: %v", err)
	}
	r.items.byUnit[unitU] = []items.Item{owed(unitU, "a")}
	if err := r.svc.Sync(ctx, []unit.Unit{unitU}); err != nil {
		t.Fatal(err)
	}
	derived := gtd.DerivedID(gtd.KindAction, unitU, "a")
	if err := r.svc.Close(ctx, derived, gtd.Done); !errors.Is(err, coregtd.ErrVerb) {
		t.Fatalf("close on a derived entry: %v", err)
	}
	if c, err := r.svc.Capture(ctx, "call Ana", "", nil); err != nil || c.Kind != gtd.KindCapture || c.State != gtd.Inbox {
		t.Fatalf("capture %+v %v", c, err)
	}
}

type outbox struct{ drafts []gtd.Entry }

func (o outbox) Entries(ctx context.Context, q gtd.Query) ([]gtd.Entry, error) { return o.drafts, nil }

func (o outbox) Entry(ctx context.Context, id gtd.ID) (gtd.Entry, bool, error) {
	for _, d := range o.drafts {
		if d.ID == id {
			return d, true, nil
		}
	}
	return gtd.Entry{}, false, nil
}

func (o outbox) Count(ctx context.Context, area string) (int, error) { return len(o.drafts), nil }

func TestRegisteredViewSource(t *testing.T) {
	ctx := context.Background()
	r := newRig()
	r.put(t, gtd.Entry{ID: "e-in", State: gtd.Inbox})
	if es, err := r.svc.Entries(ctx, gtd.Query{View: gtd.ViewOutbox}); err != nil || len(es) != 0 {
		t.Fatalf("outbox without a source: %v %v", es, err)
	}
	if c, err := r.svc.Counts(ctx, ""); err != nil || c.Views[gtd.ViewOutbox] != 0 || c.Badge != 1 {
		t.Fatalf("counts without a source: %+v %v", c, err)
	}
	if _, err := r.svc.Entry(ctx, "draft:7"); !errors.Is(err, coregtd.ErrNoEntry) {
		t.Fatalf("a draft without a source: %v", err)
	}
	src := outbox{drafts: []gtd.Entry{{ID: "draft:7", Kind: gtd.KindDraft, Origin: gtd.OriginDraft}, {ID: "draft:8", Kind: gtd.KindDraft}}}
	r.svc.RegisterView(gtd.ViewOutbox, "draft:", src)
	if es, err := r.svc.Entries(ctx, gtd.Query{View: gtd.ViewOutbox}); err != nil || len(es) != 2 {
		t.Fatalf("outbox: %v %v", es, err)
	}
	if e, err := r.svc.Entry(ctx, "draft:7"); err != nil || e.Kind != gtd.KindDraft {
		t.Fatalf("draft:7 %+v %v", e, err)
	}
	if _, err := r.svc.Entry(ctx, "draft:9"); !errors.Is(err, coregtd.ErrNoEntry) {
		t.Fatalf("draft:9: %v", err)
	}
	if c, err := r.svc.Counts(ctx, ""); err != nil || c.Views[gtd.ViewOutbox] != 2 || c.Badge != 3 {
		t.Fatalf("counts: %+v %v", c, err)
	}
	var ran []string
	r.svc.Register(gtd.KindDraft, gtd.VerbApprove, func(ctx context.Context, e gtd.Entry, req coregtd.Request) (coregtd.Result, error) {
		ran = append(ran, fmt.Sprint(e.ID, " ", req.Verb))
		return coregtd.Result{Entry: e}, nil
	})
	if _, err := r.svc.Do(ctx, "draft:7", coregtd.Request{Verb: gtd.VerbApprove}); err != nil || !slices.Equal(ran, []string{"draft:7 approve"}) {
		t.Fatalf("approve: %v %v", ran, err)
	}
	if _, err := r.svc.Do(ctx, "draft:7", coregtd.Request{Verb: gtd.VerbNext}); !errors.Is(err, coregtd.ErrVerb) {
		t.Fatalf("next on a draft: %v", err)
	}
}

func TestUserScheduleSurvivesMute(t *testing.T) {
	ctx := context.Background()
	r := newRig()
	r.items.byUnit[unitU] = []items.Item{owed(unitU, "a")}
	muteEnd := t0.Add(72 * time.Hour)
	r.facts.facts[unitU] = gtd.UnitFacts{Unit: unitU, Mute: &gtd.Mute{Until: muteEnd}}
	if err := r.svc.Sync(ctx, []unit.Unit{unitU}); err != nil {
		t.Fatal(err)
	}
	id := gtd.DerivedID(gtd.KindAction, unitU, "a")
	if e := r.entry(t, id); e.State != gtd.Scheduled || !e.Snooze.Mute {
		t.Fatalf("muted: %+v", e)
	}
	mine := time.Date(2026, 12, 1, 18, 0, 0, 0, brt)
	if _, err := r.svc.Do(ctx, id, coregtd.Request{Verb: gtd.VerbSchedule, Args: map[string]string{"until": "2026-12-01"}}); err != nil {
		t.Fatal(err)
	}
	check := func(when string) {
		t.Helper()
		e := r.entry(t, id)
		if e.State != gtd.Scheduled || !e.Snooze.Until.Equal(mine) || e.Snooze.Mute {
			t.Fatalf("%s: %+v", when, e)
		}
	}
	r.clock.t = t0.Add(time.Hour)
	if err := r.svc.Sync(ctx, []unit.Unit{unitU}); err != nil {
		t.Fatal(err)
	}
	check("still muted")
	r.facts.facts[unitU] = gtd.UnitFacts{Unit: unitU}
	r.clock.t = muteEnd.Add(time.Hour)
	if err := r.svc.Sync(ctx, []unit.Unit{unitU}); err != nil {
		t.Fatal(err)
	}
	check("unmuted")
}

func TestCloseRefusesADisabledPlugin(t *testing.T) {
	r := newRig()
	ctx := context.Background()
	e, err := r.svc.Open(ctx, coregtd.OpenRequest{Kind: gtd.KindMessage, Title: "run the round?", Unit: &unitU})
	if err != nil {
		t.Fatal(err)
	}
	r.facts.off[unit.GitHub] = true
	if err := r.svc.Close(ctx, e.ID, gtd.Done); !errors.Is(err, coregtd.ErrPluginDisabled) {
		t.Fatalf("close on a disabled plugin's entry: %v", err)
	}
	if got := r.entry(t, e.ID); got.State != gtd.Inbox {
		t.Fatalf("the entry moved to %s", got.State)
	}
}

func TestRetitleUpdatesAnOpenedEntry(t *testing.T) {
	r := newRig()
	ctx := context.Background()
	e, err := r.svc.Open(ctx, coregtd.OpenRequest{Kind: gtd.KindMessage, Title: "run the round?", Unit: &unitU})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := r.store.EntriesSince(ctx, gtd.Query{View: gtd.ViewInbox}, coregtd.Since{})
	r.clock.t = t0.Add(time.Minute)
	if err := r.svc.Retitle(ctx, e.ID, "hold the round?"); err != nil {
		t.Fatal(err)
	}
	if got := r.entry(t, e.ID); got.Title != "hold the round?" || !got.Updated.Equal(t0.Add(time.Minute)) || got.State != gtd.Inbox {
		t.Fatalf("entry %+v", got)
	}
	page, _ := r.store.EntriesSince(ctx, gtd.Query{View: gtd.ViewInbox}, coregtd.Since{Epoch: before.Epoch, Rev: before.Rev})
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
	if err := r.svc.Retitle(ctx, gtd.DerivedID(gtd.KindAction, unitU, "a"), "x"); !errors.Is(err, coregtd.ErrVerb) {
		t.Fatalf("retitle of a derived entry: %v", err)
	}
}
