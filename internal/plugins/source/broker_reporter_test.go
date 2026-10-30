package source_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/plugins/codec"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

func TestAReportKeepsTheSnapshotsOfOwnUnitsAndNamesTheRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c, err := codec.Of("acme", manifest.Codec{Keys: []string{"{thread}"}})
	if err != nil {
		t.Fatal(err)
	}
	b := &psource.Broker{ID: "acme", Units: psource.Units{Codecs: unit.NewRegistry(c)}, Snapshots: psource.NewSnapshots(db.Snapshots()), Links: db.Links()}
	h := b.Host()
	at := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	thread := src.Snapshot{Unit: plugin.Unit{Provider: "acme", Key: "t1"}, Rev: 1, At: at}
	cursor := src.Snapshot{Unit: plugin.Unit{Provider: "acme"}, Rev: 1, At: at, Gone: true}
	err = h.Reporter.Report(ctx, []src.Snapshot{cursor, thread})
	if err == nil || !strings.Contains(err.Error(), "not a unit key") {
		t.Fatalf("a report with a keyless unit: %v", err)
	}
	if _, ok, err := b.Snapshots.Last(ctx, unit.Unit{Provider: "acme", Key: "t1"}); err != nil || !ok {
		t.Fatalf("the thread's snapshot was not kept: %v %v", ok, err)
	}
}
