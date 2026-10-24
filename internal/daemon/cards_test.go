package daemon

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/domain/card"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel/cards"
)

const (
	r7Detail = "/api/units/github/acme%2Fapi%237"
	r9Detail = "/api/units/github/acme%2Fapi%239"
)

func newCardsServer(t *testing.T, enabled func(unit.ProviderID) bool) (*Server, *sqlite.DB, *coreitems.Ledger) {
	t.Helper()
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "inv.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	l := &coreitems.Ledger{Store: db.Items(), Enabled: enabled, Clock: fixedClock(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))}
	s := newServerOn(t, db, nil, func(d *Deps) {
		d.Items, d.PluginEnabled, d.Cards = l, enabled, cards.Served(db.Cards(), enabled)
		if enabled != nil && !enabled(unit.GitHub) {
			d.RowProviders = nil
		}
	})
	return s, db, l
}

func TestDisabledUnitDetailReadOnly(t *testing.T) {
	ctx := context.Background()
	off := func(p unit.ProviderID) bool { return p != unit.GitHub }
	s, db, l := newCardsServer(t, off)
	r9u := unit.Unit{Provider: unit.GitHub, Key: "acme/api#9"}
	if code := get(t, s, r9Detail, nil); code != 404 {
		t.Fatalf("a disabled unit with no card: %d, want 404", code)
	}
	at := time.Date(2026, 10, 22, 11, 0, 0, 0, time.UTC)
	c := card.Card{Unit: r9u, Title: "Batch export", State: "open", Link: "https://github.com/acme/api/pull/9"}
	if _, err := db.Cards().Save(ctx, c, at); err != nil {
		t.Fatal(err)
	}
	l.Enabled = nil
	if _, err := l.Apply(ctx, items.User, []items.Op{{Op: items.OpCreate, Unit: r9u, Item: "i1", Anchor: unit.Anchor{Ref: "user:1"}, Fields: &items.Fields{Kind: ptrOf(items.Todo), Summary: ptrOf("x")}}}); err != nil {
		t.Fatal(err)
	}
	l.Enabled = off
	var d api.Detail
	if code := get(t, s, r9Detail, &d); code != 200 {
		t.Fatalf("a disabled unit with a card: %d", code)
	}
	if d.Card == nil || !d.Card.Stale || d.Card.Card.Title != "Batch export" || len(d.Items) != 1 || d.Items[0].ID != "i1" {
		t.Errorf("detail %+v", d)
	}
	if d.Row.Unit != r9u || d.Row.Title != "Batch export" || d.Row.Link != c.Link {
		t.Errorf("row %+v, want the card's unit, title and link", d.Row)
	}
	var stored api.Detail
	if code := get(t, s, r7Detail, &stored); code != 200 || stored.Row.Title != "Batch export" || stored.Items == nil {
		t.Errorf("a disabled unit with a stored row: %d %+v", code, stored)
	}
}

func TestEnabledUnitDetailCarriesCardAndItems(t *testing.T) {
	s, db, _ := newCardsServer(t, nil)
	r7u := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	var raw map[string]json.RawMessage
	if code := get(t, s, r7Detail, &raw); code != 200 {
		t.Fatalf("%d", code)
	}
	if string(raw["items"]) != "[]" || raw["card"] != nil {
		t.Errorf("items %s, card %s", raw["items"], raw["card"])
	}
	if _, err := db.Cards().Save(context.Background(), card.Card{Unit: r7u, Title: "t"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	var d api.Detail
	if code := get(t, s, r7Detail, &d); code != 200 || d.Card == nil || d.Card.Stale || d.Row.Title != "Batch export" {
		t.Errorf("%d %+v", code, d)
	}
}

type fakeCardsJob struct {
	mu      sync.Mutex
	changes []api.Change
}

func (f *fakeCardsJob) Changed(c api.Change) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.changes = append(f.changes, c)
}

func (f *fakeCardsJob) Run(ctx context.Context, _ cards.Rows) { <-ctx.Done() }

func TestRowChangesFanOutToTheCardsJob(t *testing.T) {
	job := &fakeCardsJob{}
	s := newServerWith(t, func(d *Deps) { d.CardsJob = job })
	s.fanOutRows(api.Change{Type: "changed", Rev: 3, Units: []unit.Unit{{Provider: unit.GitHub, Key: "acme/api#7"}}})
	job.mu.Lock()
	defer job.mu.Unlock()
	if len(job.changes) != 1 || job.changes[0].Rev != 3 {
		t.Errorf("job got %+v", job.changes)
	}
}

func TestEnabledUnitWithOnlyACardIsNotFound(t *testing.T) {
	s, db, _ := newCardsServer(t, nil)
	r9u := unit.Unit{Provider: unit.GitHub, Key: "acme/api#9"}
	if _, err := db.Cards().Save(context.Background(), card.Card{Unit: r9u, Title: "lapsed"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if code := get(t, s, r9Detail, nil); code != 404 {
		t.Errorf("an enabled unit with a card and no row: %d, want 404", code)
	}
}

func ptrOf[T any](v T) *T { return &v }
