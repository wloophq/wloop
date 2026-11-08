package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// BenchmarkASweepSaveOfAThousandItems is a sweep pass rewriting the same thousand items, which compares what the
// plan serves of each before and after.
func BenchmarkASweepSaveOfAThousandItems(b *testing.B) {
	db, _ := openLogged(b, filepath.Join(b.TempDir(), "bench.db"))
	items := make([]workflow.Item, 1000)
	for i := range items {
		items[i] = pendingItem("w", unit.Unit{Provider: unit.Session, Key: fmt.Sprintf("term_%d", i)})
	}
	w := automation.PlanWrite{Workflows: []string{"w"}, Items: items}
	ctx := context.Background()
	for b.Loop() {
		if err := db.Ledger().Save(ctx, w); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkThePlanSnapshotOfAThousandItems is the read a ledger write makes twice to tell what the plan serves moved.
func BenchmarkThePlanSnapshotOfAThousandItems(b *testing.B) {
	db, _ := openLogged(b, filepath.Join(b.TempDir(), "bench.db"))
	items := make([]workflow.Item, 1000)
	for i := range items {
		items[i] = pendingItem("w", unit.Unit{Provider: unit.Session, Key: fmt.Sprintf("term_%d", i)})
	}
	ctx := context.Background()
	if err := db.Ledger().Save(ctx, automation.PlanWrite{Workflows: []string{"w"}, Items: items}); err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		if _, err := db.Write(ctx, func(tx *Tx) error { _, err := planSet(ctx, tx); return err }); err != nil {
			b.Fatal(err)
		}
	}
}
