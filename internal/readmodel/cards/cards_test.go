package cards

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/domain/card"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

const thread unit.ProviderID = "thread"

var (
	t1 = unit.Unit{Provider: thread, Key: "c/1"}
	t2 = unit.Unit{Provider: thread, Key: "c/2"}
)

type fakeRows struct {
	mu   sync.Mutex
	rev  uint64
	rows map[unit.Unit]api.Row
}

func newRows(rows ...api.Row) *fakeRows {
	f := &fakeRows{rows: map[unit.Unit]api.Row{}}
	for _, r := range rows {
		f.rows[r.Unit] = r
	}
	return f
}

func (f *fakeRows) set(r api.Row) uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rev++
	f.rows[r.Unit] = r
	return f.rev
}

func (f *fakeRows) Row(_ context.Context, u unit.Unit, _ catalog.Asked) (api.Row, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[u]
	return r, ok, nil
}

func (f *fakeRows) Rows(_ context.Context, _ api.Since) (api.RowsPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	page := api.RowsPage{Rev: f.rev, Full: true}
	for _, r := range f.rows {
		page.Rows = append(page.Rows, r)
	}
	slices.SortFunc(page.Rows, func(a, b api.Row) int { return api.CompareUnits(a.Unit, b.Unit) })
	return page, nil
}

type fakeStore struct {
	mu    sync.Mutex
	cards map[unit.Unit]card.Snapshot
	saves []card.Card
}

func (s *fakeStore) Save(_ context.Context, c card.Card, at time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cards == nil {
		s.cards = map[unit.Unit]card.Snapshot{}
	}
	s.saves = append(s.saves, c)
	if old, ok := s.cards[c.Unit]; ok && sameCard(old.Card, c) {
		return false, nil
	}
	s.cards[c.Unit] = card.Snapshot{Card: c, At: at}
	return true, nil
}

func (s *fakeStore) Get(_ context.Context, units []unit.Unit) (map[unit.Unit]card.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[unit.Unit]card.Snapshot{}
	for _, u := range units {
		if c, ok := s.cards[u]; ok {
			out[u] = c
		}
	}
	return out, nil
}

func (s *fakeStore) saved() []card.Card {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.saves)
}

type reconcile struct {
	u       unit.Unit
	desired []items.Item
	at      unit.Anchor
	absent  items.Resolution
}

type fakeLedger struct {
	mu    sync.Mutex
	calls []reconcile
	block chan struct{} // the first call waits on it when set
	in    chan struct{}
}

func (l *fakeLedger) Reconcile(_ context.Context, u unit.Unit, desired []items.Item, at unit.Anchor, absent items.Resolution) (coreitems.Result, error) {
	l.mu.Lock()
	first := len(l.calls) == 0
	l.calls = append(l.calls, reconcile{u, desired, at, absent})
	l.mu.Unlock()
	if first && l.block != nil {
		close(l.in)
		<-l.block
	}
	return coreitems.Result{}, nil
}

func (l *fakeLedger) got() []reconcile {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.calls)
}

func row(u unit.Unit, title string) api.Row {
	return api.Row{Unit: u, Title: title, Payload: json.RawMessage(`{"state":"open"}`)}
}

func cardOf(_ context.Context, r api.Row) (card.Card, error) {
	var p struct{ State string }
	_ = json.Unmarshal(r.Payload, &p)
	c := card.Card{Unit: r.Unit, Title: r.Title, State: p.State}
	switch p.State {
	case "merged":
		c.Outcome = items.Done
	case "closed":
		c.Outcome = items.Moot
	case "broken":
		return card.Card{}, errors.New("broken payload")
	}
	return c, nil
}

func itemsOf(_ context.Context, r api.Row) ([]items.Item, error) {
	return []items.Item{{ID: "x:1", Unit: r.Unit, Kind: items.Request, Summary: r.Title}}, nil
}

type logs struct {
	mu    sync.Mutex
	lines []string
}

func (l *logs) logf(f string, a ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(f, a...))
}

func (l *logs) count(sub string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, s := range l.lines {
		if strings.Contains(s, sub) {
			n++
		}
	}
	return n
}

type env struct {
	rows   *fakeRows
	store  *fakeStore
	ledger *fakeLedger
	log    *logs
	job    *Job
	cancel context.CancelFunc
	done   chan struct{}
}

func start(t *testing.T, p Provider, rows *fakeRows, ledger *fakeLedger) *env {
	t.Helper()
	if ledger == nil {
		ledger = &fakeLedger{}
	}
	e := &env{rows: rows, store: &fakeStore{}, ledger: ledger, log: &logs{}, done: make(chan struct{})}
	e.job = &Job{Store: e.store, Ledger: ledger, Providers: map[unit.ProviderID]Provider{thread: p}, Log: e.log.logf}
	ctx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	go func() {
		defer close(e.done)
		e.job.Run(ctx, rows)
	}()
	t.Cleanup(e.stop)
	return e
}

func (e *env) stop() {
	e.cancel()
	<-e.done
}

func (e *env) change(rev uint64, units ...unit.Unit) {
	e.job.Changed(api.Change{Type: "changed", Rev: rev, Units: units})
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func (e *env) idle(t *testing.T) {
	t.Helper()
	waitFor(t, "the job to go idle", e.job.idle)
}

func TestCardsJobStoresChangedCards(t *testing.T) {
	rows := newRows(row(t1, "first"))
	e := start(t, Provider{Card: cardOf}, rows, nil)
	e.idle(t)
	if got, _ := e.store.Get(context.Background(), []unit.Unit{t1}); got[t1].Card.Title != "first" {
		t.Fatalf("startup sweep stored %+v", got)
	}
	writes := len(e.store.saved())
	e.change(rows.set(row(t1, "first")), t1)
	e.idle(t)
	if n := len(e.store.saved()); n != writes {
		t.Errorf("an equal card was saved again: %d saves, want %d", n, writes)
	}
	e.change(rows.set(row(t1, "second")), t1)
	e.idle(t)
	got, _ := e.store.Get(context.Background(), []unit.Unit{t1})
	if got[t1].Card.Title != "second" {
		t.Errorf("card %+v, want title second", got[t1].Card)
	}
	if n := len(e.store.saved()); n != writes+1 {
		t.Errorf("%d saves, want %d", n, writes+1)
	}
}

func TestCardErrorKeepsSnapshot(t *testing.T) {
	rows := newRows(row(t1, "ok"), row(t2, "ok"))
	e := start(t, Provider{Card: cardOf}, rows, nil)
	e.idle(t)
	broken := api.Row{Unit: t1, Title: "bad", Payload: json.RawMessage(`{"state":"broken"}`)}
	e.change(rows.set(broken), t1)
	e.change(rows.set(broken), t1)
	e.change(rows.set(row(t2, "t2 moved")), t2)
	e.idle(t)
	got, _ := e.store.Get(context.Background(), []unit.Unit{t1, t2})
	if got[t1].Card.Title != "ok" {
		t.Errorf("t1's snapshot became %+v", got[t1].Card)
	}
	if got[t2].Card.Title != "t2 moved" {
		t.Errorf("t2 not refreshed: %+v", got[t2].Card)
	}
	if n := e.log.count("broken payload"); n != 1 {
		t.Errorf("the error was logged %d times, want once: %v", n, e.log.lines)
	}
	e.change(rows.set(row(t1, "fixed")), t1)
	e.idle(t)
	e.change(rows.set(broken), t1)
	e.idle(t)
	if n := e.log.count("broken payload"); n != 2 {
		t.Errorf("a new failure after a success was logged %d times in all, want 2", n)
	}
}

func TestCardsJobReconcilesItems(t *testing.T) {
	rows := newRows(row(t1, "a"))
	e := start(t, Provider{Card: cardOf, Items: itemsOf}, rows, nil)
	e.idle(t)
	rev := rows.set(row(t1, "b"))
	e.change(rev, t1)
	e.idle(t)
	calls := e.ledger.got()
	if len(calls) != 2 {
		t.Fatalf("%d reconciles, want 2 (startup and the change): %+v", len(calls), calls)
	}
	last := calls[1]
	want := unit.Anchor{Unit: t1, Ref: fmt.Sprintf("row:%d", rev)}
	if last.u != t1 || last.at != want || last.absent != items.Superseded || len(last.desired) != 1 || last.desired[0].Summary != "b" {
		t.Errorf("reconcile %+v, want unit %v anchor %+v absent superseded and the item of row b", last, t1, want)
	}
}

func TestCardsJobPassesOutcome(t *testing.T) {
	for state, want := range map[string]items.Resolution{"merged": items.Done, "closed": items.Moot, "open": items.Superseded} {
		t.Run(state, func(t *testing.T) {
			rows := newRows(api.Row{Unit: t1, Title: "x", Payload: json.RawMessage(`{"state":"` + state + `"}`)})
			e := start(t, Provider{Card: cardOf, Items: func(context.Context, api.Row) ([]items.Item, error) { return nil, nil }}, rows, nil)
			e.idle(t)
			calls := e.ledger.got()
			if len(calls) != 1 || calls[0].absent != want {
				t.Errorf("reconciles %+v, want one with absent %s", calls, want)
			}
		})
	}
}

func TestCardsJobUsesStoredOutcomeWhenTheCardFails(t *testing.T) {
	rows := newRows(api.Row{Unit: t1, Title: "x", Payload: json.RawMessage(`{"state":"merged"}`)})
	e := start(t, Provider{Card: cardOf, Items: func(context.Context, api.Row) ([]items.Item, error) { return nil, nil }}, rows, nil)
	e.idle(t)
	e.change(rows.set(api.Row{Unit: t1, Title: "x", Payload: json.RawMessage(`{"state":"broken"}`)}), t1)
	e.idle(t)
	calls := e.ledger.got()
	if len(calls) != 2 || calls[1].absent != items.Done {
		t.Errorf("reconciles %+v, want the stored card's outcome done", calls)
	}
}

func TestCardsJobNeverBlocksPublish(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rows := newRows(row(t1, "r0"), row(t2, "r0"))
		ledger := &fakeLedger{block: make(chan struct{}), in: make(chan struct{})}
		e := start(t, Provider{Card: cardOf, Items: itemsOf}, rows, ledger)
		<-ledger.in
		var last [2]uint64
		began := time.Now()
		for i := 1; i <= 100; i++ {
			u, k := t1, 0
			if i%2 == 0 {
				u, k = t2, 1
			}
			last[k] = rows.set(row(u, fmt.Sprintf("r%d", i)))
			e.change(last[k], u)
		}
		if d := time.Since(began); d != 0 {
			t.Errorf("100 changes took %v while a reconcile ran", d)
		}
		close(ledger.block)
		e.idle(t)
		final := map[unit.Unit]reconcile{}
		for _, c := range ledger.got() {
			final[c.u] = c
		}
		for k, u := range []unit.Unit{t1, t2} {
			want := fmt.Sprintf("r%d", 99+k)
			c := final[u]
			if len(c.desired) != 1 || c.desired[0].Summary != want || c.at.Ref != fmt.Sprintf("row:%d", last[k]) {
				t.Errorf("%v last reconciled %+v, want summary %s at row:%d", u, c, want, last[k])
			}
		}
		if n := len(ledger.got()); n > 6 {
			t.Errorf("%d reconciles for 100 changes of 2 units; the job does not coalesce", n)
		}
	})
}

func TestCardsJobSweepsOnAll(t *testing.T) {
	rows := newRows(row(t1, "a"))
	e := start(t, Provider{Card: cardOf}, rows, nil)
	e.idle(t)
	rows.set(row(t2, "b"))
	e.job.Changed(api.Change{Type: "changed", Rev: rows.rev, All: true})
	e.idle(t)
	got, _ := e.store.Get(context.Background(), []unit.Unit{t2})
	if got[t2].Card.Title != "b" {
		t.Errorf("an All change did not sweep: %+v", got)
	}
}

func TestDisabledCardsAreStale(t *testing.T) {
	off := unit.Unit{Provider: "off", Key: "k"}
	rows := newRows(row(t1, "a"), row(off, "o"))
	e := start(t, Provider{Card: cardOf, Items: itemsOf}, rows, nil)
	e.idle(t)
	e.change(rows.set(row(off, "o2")), off)
	e.idle(t)
	for _, c := range e.store.saved() {
		if c.Unit == off {
			t.Errorf("the job saved a card of a disabled plugin: %+v", c)
		}
	}
	for _, c := range e.ledger.got() {
		if c.u == off {
			t.Errorf("the job reconciled a disabled plugin's unit: %+v", c)
		}
	}
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	if _, err := e.store.Save(context.Background(), card.Card{Unit: off, Title: "kept"}, at); err != nil {
		t.Fatal(err)
	}
	served := Served(e.store, func(p unit.ProviderID) bool { return p != "off" })
	got, err := served.Get(context.Background(), []unit.Unit{t1, off})
	if err != nil {
		t.Fatal(err)
	}
	if !got[off].Stale || got[off].Card.Title != "kept" {
		t.Errorf("disabled snapshot %+v, want the stored card stale", got[off])
	}
	if got[t1].Stale {
		t.Errorf("enabled snapshot %+v is stale", got[t1])
	}
	if all := Served(e.store, nil); all != e.store {
		t.Error("Served with no switch wraps the store")
	}
}

type openItems []items.Item

func (o openItems) Query(_ context.Context, q coreitems.Query) ([]items.Item, error) {
	var out []items.Item
	for _, it := range o {
		if (q.State == "" || it.State == q.State) && (len(q.Providers) == 0 || slices.Contains(q.Providers, it.Unit.Provider)) {
			out = append(out, it)
		}
	}
	return out, nil
}

func lastOf(calls []reconcile, u unit.Unit) (reconcile, bool) {
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].u == u {
			return calls[i], true
		}
	}
	return reconcile{}, false
}

func TestCardsJobResolvesItemsOfAGoneUnit(t *testing.T) {
	t.Run("a unit leaves the rows", func(t *testing.T) {
		merged := api.Row{Unit: t2, Title: "m", Payload: json.RawMessage(`{"state":"merged"}`)}
		rows := newRows(row(t1, "a"), merged)
		e := start(t, Provider{Card: cardOf, Items: itemsOf}, rows, nil)
		e.idle(t)
		rows.mu.Lock()
		delete(rows.rows, t1)
		delete(rows.rows, t2)
		rows.rev++
		rev := rows.rev
		rows.mu.Unlock()
		e.change(rev, t1, t2)
		e.idle(t)
		want := map[unit.Unit]items.Resolution{t1: items.Superseded, t2: items.Done}
		for u, absent := range want {
			c, ok := lastOf(e.ledger.got(), u)
			if !ok || len(c.desired) != 0 || c.absent != absent || c.at.Ref != fmt.Sprintf("row:%d", rev) {
				t.Errorf("%v gone: last reconcile %+v, want no desired item, absent %s at row:%d", u, c, absent, rev)
			}
		}
	})
	t.Run("a unit gone while the daemon was down", func(t *testing.T) {
		lapsed := unit.Unit{Provider: thread, Key: "c/lapsed"}
		user := unit.Unit{Provider: thread, Key: "c/user"}
		rows := newRows(row(t1, "a"))
		e := &env{rows: rows, store: &fakeStore{}, ledger: &fakeLedger{}, log: &logs{}, done: make(chan struct{})}
		e.job = &Job{Store: e.store, Ledger: e.ledger, Providers: map[unit.ProviderID]Provider{thread: {Card: cardOf, Items: itemsOf}}, Log: e.log.logf,
			Open: openItems{
				{ID: "x:1", Unit: lapsed, State: items.Open, Provenance: items.Derived},
				{ID: "i1", Unit: user, State: items.Open, Provenance: items.User},
				{ID: "x:1", Unit: t1, State: items.Open, Provenance: items.Derived},
			}}
		ctx, cancel := context.WithCancel(context.Background())
		e.cancel = cancel
		go func() {
			defer close(e.done)
			e.job.Run(ctx, rows)
		}()
		t.Cleanup(e.stop)
		e.idle(t)
		calls := e.ledger.got()
		if c, ok := lastOf(calls, lapsed); !ok || len(c.desired) != 0 || c.absent != items.Superseded {
			t.Errorf("the lapsed unit: %+v %v", c, ok)
		}
		if c, ok := lastOf(calls, user); ok {
			t.Errorf("a unit with only a user item was reconciled: %+v", c)
		}
		if c, ok := lastOf(calls, t1); !ok || len(c.desired) != 1 {
			t.Errorf("a listed unit lost its row's items: %+v", c)
		}
	})
}
