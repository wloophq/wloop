// Package gtdtest holds an in-memory entry store and the contract suite every entry store passes.
package gtdtest

import (
	"context"
	"fmt"
	"slices"
	"sync"

	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Store is an in-memory coregtd.Store. Notes counts the Applies that changed something, Applies every call.
type Store struct {
	mu      sync.Mutex
	epoch   string
	rev     uint64
	entries map[gtd.ID]gtd.Entry
	revs    map[gtd.ID]uint64
	Notes   int
	Applies int
	Events  []port.Event
}

var _ coregtd.Store = (*Store)(nil)

func New() *Store {
	return &Store{epoch: "fake", entries: map[gtd.ID]gtd.Entry{}, revs: map[gtd.ID]uint64{}}
}

func (s *Store) all() []gtd.Entry {
	out := make([]gtd.Entry, 0, len(s.entries))
	for _, e := range s.entries {
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b gtd.Entry) int { return a.Created.Compare(b.Created) })
	return out
}

func (s *Store) Entries(ctx context.Context, q gtd.Query) ([]gtd.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return gtd.Select(s.all(), q), nil
}

func (s *Store) EntriesSince(ctx context.Context, q gtd.Query, since coregtd.Since) (coregtd.Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all := s.all()
	p := coregtd.Page{Epoch: s.epoch, Rev: s.rev, Counters: gtd.CountsOf(all, q.Area)}
	if since.Epoch != s.epoch || since.Rev > s.rev {
		p.Full, p.Entries = true, gtd.Select(all, q)
		return p, nil
	}
	p.Entries, p.Removed = coregtd.Delta(all, q, func(id gtd.ID) bool { return s.revs[id] > since.Rev })
	return p, nil
}

func (s *Store) Entry(ctx context.Context, id gtd.ID) (gtd.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[id]
	if !ok {
		return gtd.Entry{}, fmt.Errorf("%w: %s", coregtd.ErrNoEntry, id)
	}
	return e, nil
}

func (s *Store) EntriesOf(ctx context.Context, u unit.Unit) ([]gtd.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []gtd.Entry
	for _, e := range s.all() {
		if slices.Contains(e.Units, u) || slices.ContainsFunc(e.Items, func(r gtd.ItemRef) bool { return r.Unit == u }) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (s *Store) Counts(ctx context.Context, area string) (gtd.Counters, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return gtd.CountsOf(s.all(), area), nil
}

func (s *Store) Apply(ctx context.Context, cs []gtd.Change, evs ...port.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Applies++
	var changed []gtd.Entry
	for _, c := range cs {
		if c.Entry.ID == "" {
			return fmt.Errorf("an entry without an id")
		}
		if old, ok := s.entries[c.Entry.ID]; ok && old.Same(c.Entry) {
			continue
		}
		changed = append(changed, c.Entry)
	}
	if len(changed) == 0 {
		if len(cs) == 0 {
			s.Events = append(s.Events, evs...)
		}
		return nil
	}
	s.Events = append(s.Events, evs...)
	s.rev++
	for _, e := range changed {
		s.entries[e.ID], s.revs[e.ID] = e, s.rev
	}
	s.Notes++
	return nil
}
