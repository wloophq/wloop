package ghsource

import (
	"context"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/storetest"
	"github.com/wloophq/wloop/internal/core/ingest"
	"github.com/wloophq/wloop/internal/core/ingest/ingesttest"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	corelinks "github.com/wloophq/wloop/internal/core/links"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/link"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
)

func TestThreadItemResolvedByGitHubReview(t *testing.T) {
	ctx := context.Background()
	f := storetest.Empty(t)
	ledger := &coreitems.Ledger{Store: f.DB.Items(), Links: f.DB.Links(), Clock: stopped{}, Log: t.Logf}
	s := snapshot()
	s.Reviews = append(s.Reviews, pr.Review{ID: "r7", State: "APPROVED", Author: "octocat", SubmittedAt: now.Add(-time.Hour), CommitOid: "h2"})
	target := s.Ref.Unit()

	thread := unit.Unit{Provider: ingesttest.Provider, Key: "C1 0001"}
	asked := unit.Anchor{Unit: thread, Ref: "C1/0001"}
	kind, summary := items.Request, "review acme/api#7"
	res, err := ledger.Apply(ctx, items.Extracted, []items.Op{
		{Op: items.OpCreate, Unit: thread, Item: "i1", Anchor: asked, Fields: &items.Fields{Kind: &kind, Summary: &summary}},
		{Op: items.OpCreate, Unit: thread, Item: "i2", Anchor: asked, Fields: &items.Fields{Kind: &kind, Summary: &summary}},
		{Op: items.OpAssign, Unit: thread, Item: "i1", Anchor: asked, Basis: items.ExplicitMention,
			Assignees: []items.Person{{Key: "slack:U000AAA", Aliases: []string{"github:octocat"}}}},
	})
	if err != nil || len(res.Refused) > 0 {
		t.Fatalf("items: %+v %v", res.Refused, err)
	}

	svc := ingesttest.NewService("viewer-1")
	svc.Post(thread.Key, "0001", "please review acme/api#7")
	outside := pr.Ref{Owner: "acme", Repo: "api", Number: 999}.Unit()
	svc.SetLinks(thread.Key, link.Link{From: thread, To: target, Kind: link.Review, Item: "i1", Evidence: asked},
		link.Link{From: thread, To: outside, Kind: link.Merge, Item: "i2", Evidence: asked})
	reg := ingest.Registration{Source: ingesttest.Source{C: ingesttest.NewClient(svc, 100)}, Store: ingesttest.NewStore(f.Log),
		Codec: ingesttest.Codec(), Units: unit.NewRegistry(pr.Codec{}, ingesttest.Codec()), Links: f.DB.Links()}
	if rep, err := ingest.Touch(ctx, reg, []string{thread.Key}, now.Add(-2*time.Hour)); err != nil || rep.Links != 2 {
		t.Fatalf("thread pass: %+v %v", rep, err)
	}

	gen := f.Log.Gen()
	gh := &fakeGH{remaining: 4000, pages: map[string][]github.StubPage{"author:ana": {{Stubs: []github.Stub{stubOf(s)}}}},
		byRef: map[github.Ref]github.Snapshot{s.Ref: s}}
	c := newCollector(t, gh)
	c.Store = f.Inventory
	if _, _, err := ingest.RunJob(ctx, c.IngestJob(), now); err != nil {
		t.Fatal(err)
	}
	moved := f.Log.Since(gen, port.TopicSourceEvents|port.TopicLinks)

	r := &corelinks.Resolver{Links: f.DB.Links(), Events: map[unit.ProviderID]corelinks.EventReader{unit.GitHub: unitEvents{f.Inventory}},
		Ledger: ledger, Clock: stopped{}, Log: t.Logf}
	n, err := r.Run(ctx, append(moved.Units, outside))
	if err != nil || n != 1 {
		t.Fatalf("resolved %d, %v (moved %v)", n, err, moved.Units)
	}
	got, err := ledger.Items(ctx, thread)
	if err != nil || len(got) != 2 {
		t.Fatalf("items = %+v, %v", got, err)
	}
	if got[1].ID != "i2" || got[1].State != items.Open {
		t.Fatalf("the item linked outside the GitHub scope = %+v", got[1])
	}
	it := got[0]
	if it.State != items.Resolved || it.Resolution != items.Done || it.Evidence.Kind != items.EvidenceUnitEvent ||
		it.Evidence.Anchor.Ref != "review:r7" || it.Evidence.Anchor.Unit != target {
		t.Fatalf("item = %+v", it)
	}
	if n, err := r.Run(ctx, moved.Units); err != nil || n != 0 {
		t.Fatalf("second run resolved %d, %v", n, err)
	}
}
