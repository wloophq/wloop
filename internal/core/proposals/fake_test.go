package proposals

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var (
	ctx    = context.Background()
	thread = unit.Unit{Provider: "slack", Key: "C000AAA/1700000000.000100"}
	pull   = unit.Unit{Provider: unit.GitHub, Key: "o/r#1"}
	// t0 is a Thursday noon.
	t0 = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
)

type memDrafts struct {
	mu     sync.Mutex
	drafts []draft.Draft
	// beforeList runs at each List, to widen a race.
	beforeList func()
}

func (m *memDrafts) Insert(ctx context.Context, d draft.Draft) (draft.Draft, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d.ID = draft.ID(len(m.drafts) + 1)
	d.Items = slices.Clone(d.Items)
	m.drafts = append(m.drafts, d)
	return d, nil
}

func (m *memDrafts) Get(ctx context.Context, id draft.ID) (draft.Draft, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id < 1 || int(id) > len(m.drafts) {
		return draft.Draft{}, outbox.ErrNotFound
	}
	return m.drafts[id-1], nil
}

func (m *memDrafts) List(ctx context.Context, f outbox.Filter) ([]draft.Draft, error) {
	if m.beforeList != nil {
		m.beforeList()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []draft.Draft
	for i := len(m.drafts) - 1; i >= 0; i-- {
		d := m.drafts[i]
		if len(f.States) > 0 && !slices.Contains(f.States, d.State) ||
			f.Unit != nil && *f.Unit != d.Unit || f.Task != "" && f.Task != d.Task ||
			f.Rule != "" && f.Rule != d.Origin.Rule || f.Key != "" && f.Key != d.Origin.Key {
			continue
		}
		out = append(out, d)
	}
	return out, nil
}

func (m *memDrafts) Update(ctx context.Context, d draft.Draft, from draft.State) (draft.Draft, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d.ID < 1 || int(d.ID) > len(m.drafts) {
		return draft.Draft{}, outbox.ErrNotFound
	}
	if cur := m.drafts[d.ID-1]; cur.State != from || cur.Rev != d.Rev {
		return draft.Draft{}, outbox.ErrConflict
	}
	d.Rev++
	m.drafts[d.ID-1] = d
	return d, nil
}

func (m *memDrafts) all() []draft.Draft {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.drafts)
}

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

type publisher struct {
	clock *clock
	mu    sync.Mutex
	sent  []draft.ID
}

func (p *publisher) Publish(ctx context.Context, d draft.Draft) (draft.Evidence, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sent = append(p.sent, d.ID)
	return draft.Evidence{Ref: fmt.Sprintf("ts.%d", d.ID), At: p.clock.Now()}, nil
}

func (p *publisher) Verify(ctx context.Context, d draft.Draft, startedAt time.Time) (draft.Evidence, bool, error) {
	return draft.Evidence{}, false, nil
}

// ledger is an items reader that records a sent draft's evidence on its items, as the items ledger does.
type ledger struct {
	mu    sync.Mutex
	items map[unit.Unit][]items.Item
}

func (l *ledger) Items(ctx context.Context, u unit.Unit) ([]items.Item, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.items[u]), nil
}

func (l *ledger) apply(ctx context.Context, prov items.Provenance, ops []items.Op) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, op := range ops {
		cur := l.items[op.Unit]
		i := slices.IndexFunc(cur, func(o items.Item) bool { return o.ID == op.Item })
		if i < 0 || op.Op != items.OpEvidence {
			return fmt.Errorf("op %s on %s", op.Op, op.Item)
		}
		cur[i].Trail = append(cur[i].Trail, *op.Evidence)
	}
	return nil
}

type env struct {
	t        *testing.T
	clock    *clock
	drafts   *memDrafts
	pub      *publisher
	svc      *outbox.Service
	ledger   *ledger
	job      *Job
	disabled map[unit.ProviderID]bool
	facts    map[unit.Unit]task.UnitFacts
	logs     []string
}

func newEnv(t *testing.T) *env {
	e := &env{t: t, clock: &clock{now: t0}, drafts: &memDrafts{},
		ledger: &ledger{items: map[unit.Unit][]items.Item{}}, disabled: map[unit.ProviderID]bool{},
		facts: map[unit.Unit]task.UnitFacts{}}
	e.pub = &publisher{clock: e.clock}
	e.svc = &outbox.Service{
		Store: e.drafts,
		Effects: func(p unit.ProviderID) (port.Effects, error) {
			if e.disabled[p] {
				return nil, fmt.Errorf("%s: %w", p, outbox.ErrDisabled)
			}
			return e.pub, nil
		},
		Evidence:    outbox.EvidenceTo(e.ledger.apply),
		Items:       e.ledger,
		Clock:       e.clock,
		RepeatAfter: 24 * time.Hour,
	}
	e.job = &Job{Outbox: e.svc, Clock: e.clock, RepeatAfter: 24 * time.Hour,
		Enabled: func(p unit.ProviderID) bool { return !e.disabled[p] },
		Log:     func(format string, args ...any) { e.logs = append(e.logs, fmt.Sprintf(format, args...)) }}
	e.job.Facts = factsOf(e.facts)
	return e
}

func (e *env) run() Summary {
	e.t.Helper()
	sum, err := e.job.Run(ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	return sum
}

func (e *env) list(rule string, states ...draft.State) []draft.Draft {
	e.t.Helper()
	got, err := e.svc.List(ctx, outbox.Filter{Rule: rule, States: states})
	if err != nil {
		e.t.Fatal(err)
	}
	return got
}

type factsOf map[unit.Unit]task.UnitFacts

func (f factsOf) UnitFacts(ctx context.Context, u unit.Unit) (task.UnitFacts, bool, error) {
	return f[u], true, nil
}
