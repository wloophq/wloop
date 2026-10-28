package ghsource

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/storetest"
	"github.com/wloophq/wloop/internal/core/port"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
	"github.com/wloophq/wloop/internal/scope"
)

// changedSnapshots are snapshots of n open pull requests of f, each moved
// on GitHub with about 50 events the store has not seen.
func changedSnapshots(f *storetest.Fixture, n int) (map[github.Ref]github.Snapshot, []github.Ref) {
	refs := f.Open[:n]
	out := make(map[github.Ref]github.Snapshot, n)
	for i, ref := range refs {
		var s github.Snapshot
		s.Ref = ref
		s.URL = ref.URL()
		s.Title, s.Author, s.State, s.ChecksState = fmt.Sprintf("Batch %d", i), "ana", dgh.StateOpen, "SUCCESS"
		s.CreatedAt = f.Now.Add(-48 * time.Hour)
		s.UpdatedAt = f.Now.Add(time.Hour)
		at := func(k int) time.Time { return s.CreatedAt.Add(time.Duration(k) * time.Minute) }
		for k := range 20 {
			oid := fmt.Sprintf("batch-%d-%d", i, k)
			s.CommitsDated = append(s.CommitsDated, github.DatedCommit{Oid: oid, Parents: 1, At: at(k)})
			s.HeadRefOid = oid
		}
		for k := range 20 {
			s.Comments = append(s.Comments, pr.Comment{ID: fmt.Sprintf("batch-c-%d-%d", i, k), Author: "bob", CreatedAt: at(100 + k)})
		}
		for k := range 9 {
			s.Reviews = append(s.Reviews, pr.Review{ID: fmt.Sprintf("batch-r-%d-%d", i, k), State: "COMMENTED", Author: "me", SubmittedAt: at(200 + k), CommitOid: s.HeadRefOid})
		}
		out[ref] = s
	}
	return out, refs
}

func batchCollector(f *storetest.Fixture, snaps map[github.Ref]github.Snapshot) *Collector {
	return &Collector{Store: f.Inventory, GH: &fakeGH{byRef: snaps}, Now: func() time.Time { return f.Now }}
}

func TestReadBatchBudget(t *testing.T) {
	f := storetest.Seeded(t, storetest.Live)
	snaps, refs := changedSnapshots(f, 30)
	c := batchCollector(f, snaps)
	ctx, sc := metertest.Start(t, context.Background())
	var rep Report
	unread, err := c.read(ctx, refs, scope.Resolved{}, f.Now, &rep)
	if err != nil || len(unread) != 0 {
		t.Fatalf("unread %v, err %v", unread, err)
	}
	if rep.Fetched != 30 || rep.Events < 30*50 {
		t.Fatalf("fetched %d with %d events, want 30 with at least 1500", rep.Fetched, rep.Events)
	}
	metertest.Within(t, sc, metertest.Budget{
		{Res: meter.SQL, Op: "query"}: 5,
		{Res: meter.Tx}:               2,
		{Res: meter.Tx, Op: "write"}:  2,
	})
	if n := metertest.Count(sc, metertest.Limit{Res: meter.Tx, Op: "write"}); n != 2 {
		t.Fatalf("%d write transactions, want 2, the plugin's and the host's events", n)
	}
}

func TestIdleReadEmitsNoPRTopic(t *testing.T) {
	f := storetest.Seeded(t, storetest.Live)
	snaps, refs := changedSnapshots(f, 30)
	c := batchCollector(f, snaps)
	ctx := context.Background()
	if _, err := c.read(ctx, refs, scope.Resolved{}, f.Now, &Report{}); err != nil {
		t.Fatal(err)
	}
	from := f.Log.Gen()
	var rep Report
	if _, err := c.read(ctx, refs, scope.Resolved{}, f.Now.Add(time.Minute), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Events != 0 {
		t.Fatalf("re-storing added %d events", rep.Events)
	}
	if ch := f.Log.Since(from, ^port.Topic(0)); !ch.Empty() {
		t.Fatalf("re-storing the same snapshots noted %+v", ch)
	}
	stored, err := f.Inventory.PRs(ctx, refs)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range refs {
		if got := stored[ref].LastSeenAt; !got.Equal(f.Now.Add(time.Minute)) {
			t.Fatalf("%s last seen %v, want the second read", ref, got)
		}
	}
}
