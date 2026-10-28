package source

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"

	"github.com/wloophq/wloop/internal/domain/snapshot"
	"github.com/wloophq/wloop/internal/domain/unit"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// Snapshots are the last snapshot each source plugin reported of each of
// its units, kept in the store, and whether the plugin is down, which makes
// its units stale: they keep their last snapshot until the plugin reports
// again.
type Snapshots struct {
	store snapshot.Store

	mu    sync.Mutex
	down  map[unit.ProviderID]bool
	notes []func(unit.ProviderID)
}

// NewSnapshots keeps the snapshots in store.
func NewSnapshots(store snapshot.Store) *Snapshots {
	return &Snapshots{store: store, down: map[unit.ProviderID]bool{}}
}

// Put keeps each snapshot of ss, by its unit; a gone unit's is dropped.
func (s *Snapshots) Put(ctx context.Context, ss map[unit.Unit]src.Snapshot) error {
	var puts []snapshot.Stored
	var gone []unit.Unit
	for u, snap := range ss {
		if snap.Gone {
			gone = append(gone, u)
			continue
		}
		raw, err := json.Marshal(snap)
		if err != nil {
			return fmt.Errorf("snapshot of %s: %w", u, err)
		}
		puts = append(puts, snapshot.Stored{Unit: u, Rev: snap.Rev, At: snap.At, JSON: raw})
	}
	slices.SortFunc(puts, func(a, b snapshot.Stored) int { return compareUnits(a.Unit, b.Unit) })
	slices.SortFunc(gone, compareUnits)
	return s.store.Put(ctx, puts, gone)
}

// Last is u's last snapshot.
func (s *Snapshots) Last(ctx context.Context, u unit.Unit) (src.Snapshot, bool, error) {
	got, err := s.Reported(ctx, []unit.Unit{u})
	snap, ok := got[u]
	return snap, ok, err
}

// Reported are units' last snapshots; a unit with none is absent.
func (s *Snapshots) Reported(ctx context.Context, units []unit.Unit) (map[unit.Unit]src.Snapshot, error) {
	got, err := s.store.Get(ctx, units)
	if err != nil {
		return nil, err
	}
	out := make(map[unit.Unit]src.Snapshot, len(got))
	for u, st := range got {
		snap, err := Decode(st)
		if err != nil {
			return nil, err
		}
		out[u] = snap
	}
	return out, nil
}

// Decode is the snapshot st stores.
func Decode(st snapshot.Stored) (src.Snapshot, error) {
	var snap src.Snapshot
	if err := json.Unmarshal(st.JSON, &snap); err != nil {
		return src.Snapshot{}, fmt.Errorf("snapshot of %s: %w", st.Unit, err)
	}
	snap.Rev = st.Rev
	return snap, nil
}

// Units are the units of p with a snapshot, sorted.
func (s *Snapshots) Units(ctx context.Context, p unit.ProviderID) ([]unit.Unit, error) {
	return s.store.Units(ctx, p)
}

// SetDown says whether p's plugin is down.
func (s *Snapshots) SetDown(p unit.ProviderID, down bool) {
	s.mu.Lock()
	changed := s.down[p] != down
	s.down[p] = down
	notes := slices.Clone(s.notes)
	s.mu.Unlock()
	if changed {
		for _, n := range notes {
			n(p)
		}
	}
}

// Stale says whether u renders from a snapshot its plugin no longer
// stands behind: its plugin is down.
func (s *Snapshots) Stale(u unit.Unit) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.down[u.Provider]
}

// OnStale calls f with a provider whose units went stale or fresh again.
func (s *Snapshots) OnStale(f func(unit.ProviderID)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notes = append(s.notes, f)
}

func compareUnits(a, b unit.Unit) int {
	switch {
	case a.Less(b):
		return -1
	case b.Less(a):
		return 1
	}
	return 0
}

// Revs are the revs of units' last snapshots; a unit with none is absent.
func (s *Snapshots) Revs(ctx context.Context, units []unit.Unit) (map[unit.Unit]uint64, error) {
	return s.store.Revs(ctx, units)
}
