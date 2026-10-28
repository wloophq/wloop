// Package cards keeps the units' card snapshots and derived items current
// with the rows the read model publishes.
package cards

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/card"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Rows is the read model as the job reads it.
type Rows interface {
	Row(ctx context.Context, u unit.Unit, asked catalog.Asked) (api.Row, bool, error)
	Rows(ctx context.Context, view string, since api.Since) (api.RowsPage, error)
}

// Provider is what one enabled plugin derives from its rows; a nil field is
// a capability the plugin lacks.
type Provider struct {
	Card  func(ctx context.Context, row api.Row) (card.Card, error)
	Items func(ctx context.Context, row api.Row) ([]items.Item, error)
}

// OpenItems finds the open items of units that may have no row.
type OpenItems interface {
	Query(ctx context.Context, q coreitems.Query) ([]items.Item, error)
}

// Job stores each changed row's card when it differs and reconciles its
// derived items. Changed never blocks: units queue, and a unit changed
// again before its turn is read once, at its newest row. A unit with no
// row resolves its open derived items; Open, when set, lets a full sweep
// find the units that left while the daemon was down.
type Job struct {
	Store     card.Store
	Ledger    coreitems.Reconciler
	Open      OpenItems
	Providers map[unit.ProviderID]Provider
	Clock     port.Clock
	Log       func(string, ...any)

	mu      sync.Mutex
	wake    chan struct{}
	pending map[unit.Unit]uint64
	all     bool
	started bool
	busy    bool
	failed  map[unit.Unit]bool
}

func (j *Job) init() {
	if j.wake == nil {
		j.wake = make(chan struct{}, 1)
		j.pending = map[unit.Unit]uint64{}
	}
}

func (j *Job) poke() {
	select {
	case j.wake <- struct{}{}:
	default:
	}
}

// Changed queues the units of a published row change; an All change queues
// every row.
func (j *Job) Changed(c api.Change) {
	if c.Type != "changed" {
		return
	}
	j.mu.Lock()
	j.init()
	if c.All {
		j.all = true
	}
	for _, u := range c.Units {
		if _, ok := j.Providers[u.Provider]; ok {
			j.pending[u] = max(j.pending[u], c.Rev)
		}
	}
	j.mu.Unlock()
	j.poke()
}

// Run sweeps every row once, then each queued change, until ctx ends.
func (j *Job) Run(ctx context.Context, rows Rows) {
	j.mu.Lock()
	j.init()
	j.all, j.started = true, true
	j.mu.Unlock()
	j.poke()
	for {
		select {
		case <-ctx.Done():
			return
		case <-j.wake:
		}
		j.mu.Lock()
		all, pending := j.all, j.pending
		j.all, j.pending, j.busy = false, map[unit.Unit]uint64{}, true
		j.mu.Unlock()
		j.sweep(ctx, rows, all, pending)
		j.mu.Lock()
		j.busy = false
		j.mu.Unlock()
	}
}

func (j *Job) idle() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.started && !j.busy && !j.all && len(j.pending) == 0 && len(j.wake) == 0
}

type revRow struct {
	row  api.Row
	rev  uint64
	gone bool
}

func (j *Job) sweep(ctx context.Context, rows Rows, all bool, pending map[unit.Unit]uint64) {
	batch := map[unit.Unit]revRow{}
	if all {
		page, err := rows.Rows(ctx, api.ViewAll, api.Since{})
		if err != nil {
			j.logf("cards: rows: %v", err)
		}
		for _, r := range page.Rows {
			if _, ok := j.Providers[r.Unit.Provider]; ok {
				batch[r.Unit] = revRow{row: r, rev: max(page.Rev, pending[r.Unit])}
			}
		}
		if err == nil {
			for _, u := range j.openDerived(ctx) {
				if _, listed := batch[u]; !listed {
					batch[u] = revRow{rev: max(page.Rev, pending[u]), gone: true}
				}
			}
		}
	}
	for u, rev := range pending {
		if _, done := batch[u]; done {
			continue
		}
		r, ok, err := rows.Row(ctx, u, catalog.Asked{})
		if err != nil {
			j.logf("cards: row of %s: %v", u.Label(), err)
			continue
		}
		switch {
		case ok:
			batch[u] = revRow{row: r, rev: rev}
		case j.Providers[u.Provider].Items != nil:
			batch[u] = revRow{rev: rev, gone: true}
		}
	}
	if len(batch) > 0 {
		j.apply(ctx, batch)
	}
}

func (j *Job) apply(ctx context.Context, batch map[unit.Unit]revRow) {
	units := make([]unit.Unit, 0, len(batch))
	for u := range batch {
		units = append(units, u)
	}
	slices.SortFunc(units, api.CompareUnits)
	stored, err := j.Store.Get(ctx, units)
	if err != nil {
		j.logf("cards: stored cards: %v", err)
	}
	now := j.now()
	for _, u := range units {
		if ctx.Err() != nil {
			return
		}
		p, rr := j.Providers[u.Provider], batch[u]
		outcome := stored[u].Card.Outcome
		if p.Card != nil && !rr.gone {
			if c, ok := j.card(ctx, p, u, rr.row); ok {
				outcome = c.Outcome
				if old, had := stored[u]; !had || !sameCard(old.Card, c) {
					if _, err := j.Store.Save(ctx, c, now); err != nil {
						j.logf("cards: saving the card of %s: %v", u.Label(), err)
					}
				}
			}
		}
		if p.Items == nil || j.Ledger == nil {
			continue
		}
		absent := outcome
		if absent == "" {
			absent = items.Superseded
		}
		at := unit.Anchor{Unit: u, Ref: "row:" + strconv.FormatUint(rr.rev, 10)}
		var desired []items.Item
		if !rr.gone {
			var err error
			if desired, err = p.Items(ctx, rr.row); err != nil {
				j.logf("cards: items of %s: %v", u.Label(), err)
				continue
			}
		}
		if _, err := j.Ledger.Reconcile(ctx, u, desired, at, absent); err != nil {
			j.logf("cards: items of %s: %v", u.Label(), err)
		}
	}
}

// openDerived are the units of the providers with items that hold an open
// derived item.
func (j *Job) openDerived(ctx context.Context) []unit.Unit {
	if j.Open == nil {
		return nil
	}
	var q coreitems.Query
	q.State = items.Open
	for id, p := range j.Providers {
		if p.Items != nil {
			q.Providers = append(q.Providers, id)
		}
	}
	if len(q.Providers) == 0 {
		return nil
	}
	got, err := j.Open.Query(ctx, q)
	if err != nil {
		j.logf("cards: open items: %v", err)
		return nil
	}
	seen := map[unit.Unit]bool{}
	var out []unit.Unit
	for _, it := range got {
		if it.Provenance == items.Derived && !seen[it.Unit] {
			seen[it.Unit] = true
			out = append(out, it.Unit)
		}
	}
	return out
}

// card is u's card from row; a failure is logged once until the card
// derives again.
func (j *Job) card(ctx context.Context, p Provider, u unit.Unit, row api.Row) (card.Card, bool) {
	c, err := p.Card(ctx, row)
	if err != nil {
		if !j.failed[u] {
			j.logf("cards: card of %s: %v", u.Label(), err)
			if j.failed == nil {
				j.failed = map[unit.Unit]bool{}
			}
			j.failed[u] = true
		}
		return card.Card{}, false
	}
	delete(j.failed, u)
	c.Unit = u
	return c, true
}

func (j *Job) now() time.Time {
	if j.Clock == nil {
		return time.Now()
	}
	return j.Clock.Now()
}

func (j *Job) logf(format string, args ...any) {
	if j.Log != nil {
		j.Log(format, args...)
	}
}

func sameCard(a, b card.Card) bool {
	x, errA := json.Marshal(a)
	y, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(x, y)
}

// Served is s with each snapshot of a disabled plugin marked Stale; s itself
// when enabled is nil.
func Served(s card.Store, enabled func(unit.ProviderID) bool) card.Store {
	if enabled == nil {
		return s
	}
	return served{s, enabled}
}

type served struct {
	card.Store
	enabled func(unit.ProviderID) bool
}

func (s served) Get(ctx context.Context, units []unit.Unit) (map[unit.Unit]card.Snapshot, error) {
	got, err := s.Store.Get(ctx, units)
	for u, snap := range got {
		if !s.enabled(u.Provider) {
			snap.Stale = true
			got[u] = snap
		}
	}
	return got, err
}
