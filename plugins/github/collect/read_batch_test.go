package collect

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/wloophq/wloop/plugins/github/api"
	"github.com/wloophq/wloop/plugins/github/pr"
	"github.com/wloophq/wloop/plugins/github/scope"
)

// changedSnapshots are snapshots of n open pull requests of f, each moved
// on GitHub with about 50 events the store has not seen.
func changedSnapshots(f *fixture, n int) (map[api.Ref]api.Snapshot, []api.Ref) {
	refs := f.Open[:n]
	out := make(map[api.Ref]api.Snapshot, n)
	for i, ref := range refs {
		var s api.Snapshot
		s.Ref = ref
		s.URL = ref.URL()
		s.Title, s.Author, s.State, s.ChecksState = fmt.Sprintf("Batch %d", i), "ana", api.StateOpen, "SUCCESS"
		s.CreatedAt = f.Now.Add(-48 * time.Hour)
		s.UpdatedAt = f.Now.Add(time.Hour)
		at := func(k int) time.Time { return s.CreatedAt.Add(time.Duration(k) * time.Minute) }
		for k := range 20 {
			oid := fmt.Sprintf("batch-%d-%d", i, k)
			s.CommitsDated = append(s.CommitsDated, api.DatedCommit{Oid: oid, Parents: 1, At: at(k)})
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

func batchCollector(f *fixture, snaps map[api.Ref]api.Snapshot) *Collector {
	return &Collector{Store: f.Store, GH: &fakeGH{byRef: snaps}, Now: func() time.Time { return f.Now }}
}

func TestReadBatchBudget(t *testing.T) {
	f := seeded(t)
	snaps, refs := changedSnapshots(f, 30)
	c := batchCollector(f, snaps)
	ctx := context.Background()
	records := f.Host.recorded()
	var rep Report
	unread, err := c.read(ctx, refs, scope.Resolved{}, f.Now, &rep)
	if err != nil || len(unread) != 0 {
		t.Fatalf("unread %v, err %v", unread, err)
	}
	if rep.Fetched != 30 || rep.Events < 30*50 {
		t.Fatalf("fetched %d with %d events, want 30 with at least 1500", rep.Fetched, rep.Events)
	}
	if n := f.Host.recorded() - records; n != 1 {
		t.Fatalf("%d calls recording the host's events, want 1", n)
	}
}

func TestIdleReadEmitsNoPRTopic(t *testing.T) {
	f := seeded(t)
	snaps, refs := changedSnapshots(f, 30)
	c := batchCollector(f, snaps)
	ctx := context.Background()
	if _, err := c.read(ctx, refs, scope.Resolved{}, f.Now, &Report{}); err != nil {
		t.Fatal(err)
	}
	mark, events := f.Changes.mark(), f.Host.count()
	var rep Report
	if _, err := c.read(ctx, refs, scope.Resolved{}, f.Now.Add(time.Minute), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Events != 0 {
		t.Fatalf("re-storing added %d events", rep.Events)
	}
	if got := f.Changes.since(mark); len(got) != 0 || f.Host.count() != events {
		t.Fatalf("re-storing the same snapshots changed %v and recorded %d events", got, f.Host.count()-events)
	}
	stored, err := f.Store.PRs(ctx, refs)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range refs {
		if got := stored[ref].LastSeenAt; !got.Equal(f.Now.Add(time.Minute)) {
			t.Fatalf("%s last seen %v, want the second read", ref, got)
		}
	}
}
