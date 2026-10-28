package outbox

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type memStore struct {
	mu     sync.Mutex
	drafts []draft.Draft
	// beforeUpdate runs inside Update before the compare, to stage a race.
	beforeUpdate func(d draft.Draft)
}

func (m *memStore) Insert(ctx context.Context, d draft.Draft) (draft.Draft, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d.ID = draft.ID(len(m.drafts) + 1)
	d.Items = slices.Clone(d.Items)
	m.drafts = append(m.drafts, d)
	return d, nil
}

func (m *memStore) Get(ctx context.Context, id draft.ID) (draft.Draft, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id < 1 || int(id) > len(m.drafts) {
		return draft.Draft{}, ErrNotFound
	}
	return m.drafts[id-1], nil
}

func (m *memStore) List(ctx context.Context, f Filter) ([]draft.Draft, error) {
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

func (m *memStore) Update(ctx context.Context, d draft.Draft, from draft.State) (draft.Draft, error) {
	if m.beforeUpdate != nil {
		m.beforeUpdate(d)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if d.ID < 1 || int(d.ID) > len(m.drafts) {
		return draft.Draft{}, ErrNotFound
	}
	if cur := m.drafts[d.ID-1]; cur.State != from || cur.Rev != d.Rev {
		return draft.Draft{}, ErrConflict
	}
	d.Rev++
	m.drafts[d.ID-1] = d
	return d, nil
}

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

type fakeEffects struct {
	mu        sync.Mutex
	published []draft.ID
	verified  []draft.ID
	failWith  error
	found     bool
	verifyErr error
	refuse    error
	// during runs inside Publish, before it returns.
	during func(d draft.Draft)
}

func (e *fakeEffects) Publish(ctx context.Context, d draft.Draft) (draft.Evidence, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.published = append(e.published, d.ID)
	if e.during != nil {
		e.during(d)
	}
	if e.failWith != nil {
		return draft.Evidence{}, e.failWith
	}
	return draft.Evidence{Ref: fmt.Sprintf("ts.%d", d.ID), Permalink: fmt.Sprintf("https://example.test/p/%d", d.ID)}, nil
}

func (e *fakeEffects) Verify(ctx context.Context, d draft.Draft, startedAt time.Time) (draft.Evidence, bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.verified = append(e.verified, d.ID)
	if e.verifyErr != nil || !e.found {
		return draft.Evidence{}, false, e.verifyErr
	}
	return draft.Evidence{Ref: fmt.Sprintf("found.%d", d.ID), At: startedAt.Add(time.Second)}, true, nil
}

type validating struct {
	*fakeEffects
}

func (v validating) Validate(ctx context.Context, d draft.Draft) error { return v.refuse }

type fakeItems map[unit.Unit][]items.Item

func (f fakeItems) Items(ctx context.Context, u unit.Unit) ([]items.Item, error) { return f[u], nil }

var (
	thread = unit.Unit{Provider: "slack", Key: "C000AAA/1.2"}
	pull   = unit.Unit{Provider: unit.GitHub, Key: "o/r#1"}
	t0     = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
)

type env struct {
	svc      *Service
	store    *memStore
	fx       *fakeEffects
	clock    *fakeClock
	disabled map[unit.ProviderID]bool
	logMu    sync.Mutex
	logs     []string
}

func newEnv() *env {
	e := &env{store: &memStore{}, fx: &fakeEffects{}, clock: &fakeClock{now: t0}, disabled: map[unit.ProviderID]bool{}}
	e.svc = &Service{
		Store: e.store,
		Effects: func(p unit.ProviderID) (port.Effects, error) {
			if e.disabled[p] {
				return nil, fmt.Errorf("%s: %w", p, ErrDisabled)
			}
			return e.fx, nil
		},
		Clock: e.clock,
		Posts: func(d draft.Delivery) bool { return d == draft.CommentDelivery },
		Log: func(format string, args ...any) {
			e.logMu.Lock()
			defer e.logMu.Unlock()
			e.logs = append(e.logs, fmt.Sprintf(format, args...))
		},
	}
	return e
}

func (e *env) propose(p Proposal) draft.Draft {
	d, err := e.svc.Propose(context.Background(), p)
	if err != nil {
		panic(err)
	}
	return d
}

func (e *env) get(id draft.ID) draft.Draft {
	d, err := e.store.Get(context.Background(), id)
	if err != nil {
		panic(err)
	}
	return d
}

func reply(text string) Proposal {
	return Proposal{Unit: thread, Kind: draft.Reply, Text: text, Origin: draft.Origin{Kind: draft.OriginTurn, TurnSeq: 4, Intent: "reply"}}
}

var errBoom = errors.New("boom")
