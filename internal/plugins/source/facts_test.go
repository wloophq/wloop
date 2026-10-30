package source_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/automation/automationtest"
	"github.com/wloophq/wloop/internal/domain/unit"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// reportedFacts are UnitFacts over a real store, holding two units acme
// reported, and a unit of another plugin.
type reportedFacts struct {
	psource.UnitFacts
	revs map[unit.Unit]uint64
}

func acmeReport(key string, rev uint64) src.Snapshot {
	since := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	return src.Snapshot{Unit: plugin.Unit{Provider: "acme", Key: key}, Rev: rev, At: since,
		Facts: []src.Fact{{Name: "acme.fingerprint", Value: src.String(key)}, {Name: "acme.repo", Value: src.String("acme/api")},
			{Name: "acme.comments", Value: src.Int(3)}, {Name: "acme.labels", Value: src.List("bug")},
			{Name: "acme.waiting", Value: src.Since(since)}, {Name: "acme.late", Value: src.After(since, time.Hour, src.WorkingDays)}}}
}

func newReportedFacts(t *testing.T) *reportedFacts {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	snaps := psource.NewSnapshots(db.Snapshots())
	f := &reportedFacts{UnitFacts: psource.UnitFacts{Snapshots: snaps}, revs: map[unit.Unit]uint64{}}
	for _, k := range []string{"two", "one"} {
		f.Report(t, unit.Unit{Provider: "acme", Key: k})
	}
	other := src.Snapshot{Unit: plugin.Unit{Provider: "wiki", Key: "page"}, Rev: 1,
		Facts: []src.Fact{{Name: "wiki.fingerprint", Value: src.String("p")}, {Name: "wiki.repo", Value: src.String("")}}}
	if err := snaps.Put(context.Background(), map[unit.Unit]src.Snapshot{{Provider: "wiki", Key: "page"}: other}); err != nil {
		t.Fatal(err)
	}
	return f
}

// Report has acme report u again, at the next rev.
func (f *reportedFacts) Report(t *testing.T, u unit.Unit) {
	f.revs[u]++
	if err := f.Snapshots.Put(context.Background(), map[unit.Unit]src.Snapshot{u: acmeReport(u.Key, f.revs[u])}); err != nil {
		t.Fatal(err)
	}
}

func TestReportedFactsAreAFactsProvider(t *testing.T) {
	automationtest.RunUnits(t, "acme", func(t *testing.T) automation.Units { return newReportedFacts(t) })
	automationtest.RunRevisioner(t, "acme", func(t *testing.T) automationtest.RevisionerBackend { return newReportedFacts(t) })
}
