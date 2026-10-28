package source

import (
	"slices"
	"sync"

	"github.com/wloophq/wloop/internal/domain/unit"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// Snapshots are the last snapshot each source plugin reported of each of
// its units, and whether the plugin is down, which makes its units stale:
// they keep their last snapshot until the plugin reports again.
type Snapshots struct {
	mu    sync.Mutex
	last  map[unit.Unit]src.Snapshot
	down  map[unit.ProviderID]bool
	notes []func(unit.ProviderID)
}

// NewSnapshots is an empty Snapshots.
func NewSnapshots() *Snapshots {
	return &Snapshots{last: map[unit.Unit]src.Snapshot{}, down: map[unit.ProviderID]bool{}}
}

// Put keeps each snapshot of ss, by its unit; a gone unit's is dropped.
func (s *Snapshots) Put(ss map[unit.Unit]src.Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for u, snap := range ss {
		if snap.Gone {
			delete(s.last, u)
			continue
		}
		s.last[u] = snap
	}
}

// Last is u's last snapshot.
func (s *Snapshots) Last(u unit.Unit) (src.Snapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, ok := s.last[u]
	return snap, ok
}

// Units are the units of p with a snapshot, sorted.
func (s *Snapshots) Units(p unit.ProviderID) []unit.Unit {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []unit.Unit
	for u := range s.last {
		if u.Provider == p {
			out = append(out, u)
		}
	}
	slices.SortFunc(out, func(a, b unit.Unit) int {
		if a.Less(b) {
			return -1
		}
		if b.Less(a) {
			return 1
		}
		return 0
	})
	return out
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
