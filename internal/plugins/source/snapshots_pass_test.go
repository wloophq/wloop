package source_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/snapshot"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

type countingSnapshots struct {
	stored map[unit.Unit]snapshot.Stored
	gets   int
}

func (s *countingSnapshots) Put(context.Context, []snapshot.Stored, []unit.Unit) error { return nil }

func (s *countingSnapshots) Get(_ context.Context, units []unit.Unit) (map[unit.Unit]snapshot.Stored, error) {
	s.gets++
	out := map[unit.Unit]snapshot.Stored{}
	for _, u := range units {
		if st, ok := s.stored[u]; ok {
			out[u] = st
		}
	}
	return out, nil
}

func (s *countingSnapshots) Revs(context.Context, []unit.Unit) (map[unit.Unit]uint64, error) {
	return nil, nil
}

func (s *countingSnapshots) Units(context.Context, unit.ProviderID) ([]unit.Unit, error) {
	return nil, nil
}

func TestAPassReadsTheSnapshotsOnce(t *testing.T) {
	a, b, c := unit.Unit{Provider: "acme", Key: "1"}, unit.Unit{Provider: "acme", Key: "2"}, unit.Unit{Provider: "acme", Key: "3"}
	store := &countingSnapshots{stored: map[unit.Unit]snapshot.Stored{}}
	for i, u := range []unit.Unit{a, b, c} {
		raw, _ := json.Marshal(src.Snapshot{Extraction: &src.ExtractionHealth{Oldest: time.Date(2026, 3, 1+i, 0, 0, 0, 0, time.UTC)}})
		store.stored[u] = snapshot.Stored{Unit: u, Rev: uint64(i + 1), JSON: raw}
	}
	snaps := psource.NewSnapshots(store)
	pass := &coretasks.Pass{Tasks: []task.Task{{ID: "t-1", Units: []unit.Unit{a}}, {ID: "t-2", Units: []unit.Unit{b}}}}
	ctx := coretasks.WithPass(context.Background(), pass)
	for _, u := range []unit.Unit{a, b, a} {
		snap, ok, err := snaps.Last(ctx, u)
		if err != nil || !ok || snap.Rev != store.stored[u].Rev {
			t.Fatalf("%s: %+v %v %v", u, snap, ok, err)
		}
	}
	if oldest, err := snaps.Unextracted(ctx, []unit.Unit{a, b}); err != nil || !oldest.Equal(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("unextracted: %v %v", oldest, err)
	}
	if store.gets != 1 {
		t.Fatalf("the pass read the store %d times", store.gets)
	}
	if snap, ok, err := snaps.Last(ctx, c); err != nil || !ok || snap.Rev != 3 || store.gets != 2 {
		t.Fatalf("a unit outside the pass: %+v %v %v, %d reads", snap, ok, err, store.gets)
	}
	if _, _, err := snaps.Last(context.Background(), a); err != nil || store.gets != 3 {
		t.Fatalf("outside a pass: %v, %d reads", err, store.gets)
	}
}
