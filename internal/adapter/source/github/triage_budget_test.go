package ghsource

import (
	"context"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/storetest"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

func TestTriageBudget(t *testing.T) {
	f := storetest.Seeded(t, storetest.Live)
	ctx, sc := metertest.Start(t, context.Background())
	stored, err := f.Inventory.PRs(ctx, append(append([]github.Ref{}, f.Open...), f.Closed...)[:120])
	if err != nil {
		t.Fatal(err)
	}
	stubs := make([]github.Stub, 0, len(stored))
	for _, p := range stored {
		s := github.Stub{Ref: p.Ref, UpdatedAt: p.UpdatedAt, ChecksState: p.ChecksState}
		for _, c := range p.FailingChecks {
			s.FailedChecks = append(s.FailedChecks, c.Name)
		}
		stubs = append(stubs, s)
	}
	c := &Collector{Store: f.Inventory}
	sql, tx := metertest.Limit{Res: meter.SQL}, metertest.Limit{Res: meter.Tx}

	moved := make([]github.Stub, len(stubs))
	for i, s := range stubs {
		s.UpdatedAt = s.UpdatedAt.Add(time.Hour)
		moved[i] = s
	}
	sqlBefore := metertest.Count(sc, sql)
	changed, err := c.triage(ctx, moved, now)
	if err != nil || len(changed) != 120 {
		t.Fatalf("changed %d, err %v", len(changed), err)
	}
	if n := metertest.Count(sc, sql) - sqlBefore; n != 1 {
		t.Fatalf("%d statements triaging 120 changed stubs, want 1\n%s", n, metertest.Report(sc.Tally()))
	}

	txBefore := metertest.Count(sc, tx)
	changed, err = c.triage(ctx, stubs, now)
	if err != nil || len(changed) != 0 {
		t.Fatalf("changed %v, err %v", changed, err)
	}
	if n := metertest.Count(sc, tx) - txBefore; n != 1 {
		t.Fatalf("%d transactions triaging 120 unchanged stubs, want the TouchStubs write only\n%s", n, metertest.Report(sc.Tally()))
	}
}
