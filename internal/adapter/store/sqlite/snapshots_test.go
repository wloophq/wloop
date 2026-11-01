package sqlite

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/snapshot"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestASnapshotReportedAgainAtItsRevNotesNoChange(t *testing.T) {
	ctx := context.Background()
	db, log := openLogged(t, filepath.Join(t.TempDir(), "store.db"))
	u := unit.Unit{Provider: "acme", Key: "one"}
	at := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	put := func(rev uint64, at time.Time) port.Generation {
		t.Helper()
		g := log.Gen()
		if err := db.Snapshots().Put(ctx, []snapshot.Stored{{Unit: u, Rev: rev, At: at, JSON: []byte(`{"n":1}`)}}, nil); err != nil {
			t.Fatal(err)
		}
		return g
	}
	put(1, at)
	if c := log.Since(put(1, at.Add(time.Minute)), port.TopicPRs); !c.Empty() {
		t.Errorf("a report at the same rev noted %+v", c)
	}
	if got, _ := db.Snapshots().Get(ctx, []unit.Unit{u}); !got[u].At.Equal(at.Add(time.Minute)) {
		t.Errorf("the report at the same rev kept at %v", got[u].At)
	}
	if c := log.Since(put(2, at.Add(2*time.Minute)), port.TopicPRs); !slices.Contains(c.Units, u) {
		t.Errorf("a report at a new rev noted %+v", c)
	}
}
