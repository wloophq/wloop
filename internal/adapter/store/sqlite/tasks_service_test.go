package sqlite_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type entryClock struct{ t time.Time }

func (c *entryClock) Now() time.Time { return c.t }

type entryItems map[unit.Unit][]items.Item

func (m entryItems) Items(ctx context.Context, u unit.Unit) ([]items.Item, error) { return m[u], nil }

type entryFacts struct {
	facts map[unit.Unit]task.UnitFacts
	off   bool
}

func (f *entryFacts) UnitFacts(ctx context.Context, u unit.Unit) (task.UnitFacts, bool, error) {
	if f.off {
		return task.UnitFacts{}, false, nil
	}
	fs := f.facts[u]
	fs.Unit = u
	return fs, true, nil
}

type entryRig struct {
	svc   *coretasks.Service
	store coretasks.Store
	items entryItems
	facts *entryFacts
	clock *entryClock
}

var (
	meKey  = items.Person{Key: "github:me"}
	anaKey = items.Person{Key: "github:ana", Name: "Ana"}
)

func newEntryRig(t testing.TB) *entryRig {
	store, _ := openEntries(t.(*testing.T))
	r := &entryRig{store: store, items: entryItems{}, facts: &entryFacts{facts: map[unit.Unit]task.UnitFacts{}}, clock: &entryClock{entryAt}}
	r.svc = coretasks.New(coretasks.Deps{Store: store, Items: r.items, Facts: r.facts, Me: task.Me{Keys: []string{"github:me"}},
		Clock: r.clock, StaleWaitingDays: 2})
	return r
}

func prUnit(n int) unit.Unit {
	return unit.Unit{Provider: unit.GitHub, Key: fmt.Sprintf("acme/api#%d", n)}
}

func TestServiceOverSqliteReinboxesOnActivity(t *testing.T) {
	t.Parallel()
	r := newEntryRig(t)
	ctx := context.Background()
	u := prUnit(1)
	r.items[u] = []items.Item{
		{ID: "a", Unit: u, Summary: "review", State: "open", Requester: anaKey, WaitingOn: []items.Person{meKey}, CreatedAt: entryAt},
		{ID: "b", Unit: u, Summary: "reply", State: "open", Requester: meKey, WaitingOn: []items.Person{anaKey}, CreatedAt: entryAt},
	}
	if err := r.svc.Sync(ctx, []unit.Unit{u}); err != nil {
		t.Fatal(err)
	}
	action, waiting := task.DerivedID(task.KindAction, u, "a"), task.DerivedID(task.KindWaiting, u, anaKey.Key)
	r.clock.t = entryAt.Add(time.Hour)
	if _, err := r.svc.Do(ctx, action, coretasks.Request{Verb: task.VerbSchedule, Args: map[string]string{"on_activity": "true"}}); err != nil {
		t.Fatal(err)
	}
	r.facts.facts[u] = task.UnitFacts{LastHuman: entryAt.Add(2 * time.Hour)}
	r.clock.t = entryAt.Add(3 * time.Hour)
	if err := r.svc.Sync(ctx, []unit.Unit{u}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []task.ID{action, waiting} {
		e, err := r.store.Entry(ctx, id)
		if err != nil || e.State != task.Inbox || !e.Changed {
			t.Fatalf("%s: %+v %v", id, e, err)
		}
	}
	r.facts.off = true
	if _, err := r.svc.Do(ctx, action, coretasks.Request{Verb: task.VerbDone}); err == nil {
		t.Fatal("a disabled plugin's entry moved")
	}
}

// TestEntriesCost logs what Sync and the reads cost at a realistic scale; it gates nothing.
func TestEntriesCost(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("cost measurement")
	}
	r := newEntryRig(t)
	ctx := context.Background()
	const units = 500
	var all []unit.Unit
	for n := range units {
		u := prUnit(n)
		all = append(all, u)
		for i := range 4 {
			it := items.Item{ID: fmt.Sprint("i", i), Unit: u, Summary: "s", State: "open", Requester: anaKey,
				WaitingOn: []items.Person{meKey}, Contexts: []string{"@reply"}, CreatedAt: entryAt}
			if i >= 2 {
				it.Requester, it.WaitingOn = meKey, []items.Person{{Key: fmt.Sprint("github:p", i)}}
			}
			r.items[u] = append(r.items[u], it)
		}
	}
	start := time.Now()
	if err := r.svc.Sync(ctx, all); err != nil {
		t.Fatal(err)
	}
	full := time.Since(start)
	open, _ := r.store.Entries(ctx, task.Query{View: task.ViewOpen})
	r.clock.t = entryAt.Add(time.Minute)
	start = time.Now()
	if err := r.svc.Sync(ctx, all[:1]); err != nil {
		t.Fatal(err)
	}
	one := time.Since(start)
	start = time.Now()
	next, err := r.store.Entries(ctx, task.Query{View: task.ViewInbox})
	if err != nil {
		t.Fatal(err)
	}
	list := time.Since(start)
	start = time.Now()
	p, err := r.store.EntriesSince(ctx, task.Query{View: task.ViewInbox}, coretasks.Since{})
	if err != nil {
		t.Fatal(err)
	}
	page := time.Since(start)
	start = time.Now()
	if _, err := r.store.EntriesSince(ctx, task.Query{View: task.ViewInbox}, coretasks.Since{Epoch: p.Epoch, Rev: p.Rev}); err != nil {
		t.Fatal(err)
	}
	delta := time.Since(start)
	start = time.Now()
	if _, err := r.store.Counts(ctx, ""); err != nil {
		t.Fatal(err)
	}
	counts := time.Since(start)
	t.Logf("%d units, %d open entries: first Sync of all %v, Sync of one unit %v, Inbox list (%d) %v, full page %v, empty delta %v, counts %v",
		units, len(open), full, one, len(next), list, page, delta, counts)
}
