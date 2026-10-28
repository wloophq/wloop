package sqlite

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/snapshot"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestV7SnapshotsCreatesTheTable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := v6Store(t)
	db := migrateV6(t, path)
	var cols []string
	for _, c := range liveTables(t, rawDB(t, path))["unit_snapshots"] {
		cols = append(cols, c.name)
	}
	if want := []string{"provider", "unit_key", "snapshot", "rev", "at"}; !slices.Equal(cols, want) {
		t.Fatalf("unit_snapshots columns %v, want %v", cols, want)
	}

	pr := unit.Unit{Provider: "acme", Key: "acme/api#7"}
	thread := unit.Unit{Provider: "acme", Key: "T1/C1/1.1"}
	other := unit.Unit{Provider: "chat", Key: "C9"}
	at := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	put := []snapshot.Stored{
		{Unit: pr, Rev: 3, At: at, JSON: []byte(`{"rev":3}`)},
		{Unit: thread, Rev: 1, At: at, JSON: []byte(`{"rev":1}`)},
		{Unit: other, Rev: 9, At: at, JSON: []byte(`{}`)},
	}
	if err := db.Snapshots().Put(ctx, put, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.Snapshots().Put(ctx, []snapshot.Stored{{Unit: pr, Rev: 4, At: at.Add(time.Minute), JSON: []byte(`{"rev":4}`)}}, []unit.Unit{thread}); err != nil {
		t.Fatal(err)
	}
	units, err := db.Snapshots().Units(ctx, "acme")
	if err != nil || !slices.Equal(units, []unit.Unit{pr}) {
		t.Fatalf("acme's units %v, %v; want the thread gone", units, err)
	}
	got, err := db.Snapshots().Get(ctx, []unit.Unit{pr, thread, other})
	if err != nil {
		t.Fatal(err)
	}
	if s := got[pr]; s.Rev != 4 || !s.At.Equal(at.Add(time.Minute)) || string(s.JSON) != `{"rev":4}` {
		t.Errorf("the replaced snapshot: %+v", s)
	}
	if _, ok := got[thread]; ok || len(got) != 2 {
		t.Errorf("snapshots %v; want the gone thread left out", got)
	}
	revs, err := db.Snapshots().Revs(ctx, []unit.Unit{pr, thread, other})
	if err != nil || revs[pr] != 4 || revs[other] != 9 || len(revs) != 2 {
		t.Errorf("revs %v, %v", revs, err)
	}
}
