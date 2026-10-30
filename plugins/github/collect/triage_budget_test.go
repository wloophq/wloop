package collect

import (
	"context"
	"testing"
	"time"

	"github.com/wloophq/wloop/plugins/github/api"
)

func TestTriageBudget(t *testing.T) {
	f := seeded(t)
	ctx := context.Background()
	stored, err := f.Store.PRs(ctx, append(append([]api.Ref{}, f.Open...), f.Closed...)[:120])
	if err != nil {
		t.Fatal(err)
	}
	stubs := make([]api.Stub, 0, len(stored))
	for _, p := range stored {
		s := api.Stub{Ref: p.Ref, UpdatedAt: p.UpdatedAt, ChecksState: p.ChecksState}
		for _, c := range p.FailingChecks {
			s.FailedChecks = append(s.FailedChecks, c.Name)
		}
		stubs = append(stubs, s)
	}
	c := &Collector{Store: f.Store}

	moved := make([]api.Stub, len(stubs))
	for i, s := range stubs {
		s.UpdatedAt = s.UpdatedAt.Add(time.Hour)
		moved[i] = s
	}
	changed, err := c.triage(ctx, moved, now)
	if err != nil || len(changed) != 120 {
		t.Fatalf("changed %d, err %v", len(changed), err)
	}

	changed, err = c.triage(ctx, stubs, now)
	if err != nil || len(changed) != 0 {
		t.Fatalf("changed %v, err %v", changed, err)
	}
	seen, err := f.Store.Seen(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range f.Open {
		if !seen[ref].Equal(now) {
			t.Fatalf("%s seen %v, want the triage's touch", ref, seen[ref])
		}
	}
}
