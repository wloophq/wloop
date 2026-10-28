package collect

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	sdk "github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/api"
	"github.com/wloophq/wloop/plugins/github/pr"
	"github.com/wloophq/wloop/plugins/github/scope"
)

func TestFastSweepFetchesChangedByRef(t *testing.T) {
	s := snapshot()
	gh := &fakeGH{remaining: 4000, pages: map[string][]api.StubPage{"author:ana": {{Stubs: []api.Stub{stubOf(s)}}}}, byRef: map[api.Ref]api.Snapshot{s.Ref: s}}
	c := newCollector(t, gh)
	rep, err := c.Fast(context.Background())
	if err != nil || rep.Stubs != 1 || rep.Fetched != 1 || rep.Events == 0 {
		t.Fatalf("%+v %v", rep, err)
	}
	pr, ok, _ := c.Store.PR(context.Background(), s.Ref)
	if !ok || pr.Title != s.Title || pr.Reasons[0] != "author" || pr.ReviewRequests[0] != "me" || pr.LastSeenAt.IsZero() {
		t.Fatalf("%+v", pr)
	}
	if pr.ChangedFiles != s.ChangedFiles || !pr.BodyEditedAt.Equal(s.LastEditedAt) {
		t.Fatalf("edited %v, files %d", pr.BodyEditedAt, pr.ChangedFiles)
	}
	gh.served = nil
	rep, _ = c.Fast(context.Background())
	if rep.Fetched != 0 || gh.byRefCalls != 1 {
		t.Fatalf("unchanged PR fetched again: %+v (%d by-ref calls)", rep, gh.byRefCalls)
	}
}

func TestFastSweepUsesUpdatedSinceAfterFirstRun(t *testing.T) {
	gh := &fakeGH{remaining: 4000}
	c := newCollector(t, gh)
	c.Fast(context.Background())
	if strings.Contains(gh.calls[0], "updated:") {
		t.Fatalf("first run must not filter: %s", gh.calls[0])
	}
	gh.calls = nil
	c.Fast(context.Background())
	if !strings.Contains(gh.calls[0], "updated:>=2026-10-05T09:55:00Z") {
		t.Fatalf("second run must filter from the last sweep minus 5m: %s", gh.calls[0])
	}
}

func TestFastSweepSeesPullRequestsClosedOrMergedSinceTheLastSweep(t *testing.T) {
	ctx := context.Background()
	closed, merged := snapshot(), snapshot()
	merged.Number = 8
	gh := &fakeGH{remaining: 4000, honorSince: true, closed: map[api.Ref]bool{},
		pages: map[string][]api.StubPage{"author:ana": {{Stubs: []api.Stub{stubOf(closed), stubOf(merged)}}}},
		byRef: map[api.Ref]api.Snapshot{closed.Ref: closed, merged.Ref: merged}}
	c := newCollector(t, gh)
	if _, err := c.Fast(ctx); err != nil {
		t.Fatal(err)
	}
	later := now.Add(2 * time.Minute)
	closed.State, closed.UpdatedAt, closed.ClosedAt = "CLOSED", later, later
	merged.State, merged.UpdatedAt, merged.ClosedAt, merged.MergedAt = "MERGED", later, later, later
	gh.byRef = map[api.Ref]api.Snapshot{closed.Ref: closed, merged.Ref: merged}
	gh.closed = map[api.Ref]bool{closed.Ref: true, merged.Ref: true}
	gh.pages["author:ana"] = []api.StubPage{{Stubs: []api.Stub{stubOf(closed), stubOf(merged)}}}
	gh.served = nil
	c.Now = func() time.Time { return later }
	if _, err := c.Fast(ctx); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		snap  api.Snapshot
		state string
		kind  string
		at    func(pr.PR) time.Time
	}{
		{closed, "CLOSED", pr.KindClosed, func(p pr.PR) time.Time { return p.ClosedAt }},
		{merged, "MERGED", pr.KindMerged, func(p pr.PR) time.Time { return p.MergedAt }},
	} {
		p, _, _ := c.Store.PR(ctx, tc.snap.Ref)
		if p.State != tc.state || !tc.at(p).Equal(later) {
			t.Errorf("%s: state %q, closed %v, merged %v", tc.state, p.State, p.ClosedAt, p.MergedAt)
		}
		evs, _ := eventsOf(ctx, c, tc.snap.Ref)
		if !slices.ContainsFunc(evs, func(e pr.Event) bool { return e.Kind == tc.kind && e.At.Equal(later) }) {
			t.Errorf("%s: no %s event in %+v", tc.state, tc.kind, evs)
		}
	}
	if open, _ := c.Store.ListPRs(ctx, true); len(open) != 0 {
		t.Errorf("still open: %+v", open)
	}
}

func TestFastSweepIgnoresUnknownClosedOrMergedPullRequests(t *testing.T) {
	ctx := context.Background()
	closed, merged := snapshot(), snapshot()
	merged.Number = 8
	closed.State, closed.ClosedAt = "CLOSED", now
	merged.State, merged.ClosedAt, merged.MergedAt = "MERGED", now, now
	closed.UpdatedAt, merged.UpdatedAt = now, now
	gh := &fakeGH{remaining: 4000, honorSince: true,
		byRef: map[api.Ref]api.Snapshot{closed.Ref: closed, merged.Ref: merged}}
	c := newCollector(t, gh)
	if _, err := c.Fast(ctx); err != nil {
		t.Fatal(err)
	}
	gh.pages = map[string][]api.StubPage{"author:ana": {{Stubs: []api.Stub{stubOf(closed), stubOf(merged)}}}}
	c.Now = func() time.Time { return now.Add(2 * time.Minute) }
	rep, err := c.Fast(ctx)
	if err != nil || rep.Stubs != 2 {
		t.Fatalf("%+v %v", rep, err)
	}
	if gh.byRefCalls != 0 || rep.Fetched != 0 {
		t.Fatalf("closed history read in full: %+v (%d by-ref calls)", rep, gh.byRefCalls)
	}
	if prs, _ := c.Store.ListPRs(ctx, false); len(prs) != 0 {
		t.Fatalf("closed history stored: %+v", prs)
	}
}

func TestFastSweepPagesSearch(t *testing.T) {
	s1, s2, s3 := snapshot(), snapshot(), snapshot()
	s2.Number, s3.Number = 8, 9
	gh := &fakeGH{remaining: 4000, pages: map[string][]api.StubPage{"author:ana": {
		{Stubs: []api.Stub{stubOf(s1)}, HasNext: true, EndCursor: "p2"},
		{Stubs: []api.Stub{stubOf(s2)}, HasNext: true, EndCursor: "p3"},
		{Stubs: []api.Stub{stubOf(s3)}},
	}}, byRef: map[api.Ref]api.Snapshot{s1.Ref: s1, s2.Ref: s2, s3.Ref: s3}}
	c := newCollector(t, gh)
	rep, _ := c.Fast(context.Background())
	if rep.Stubs != 2 || len(rep.Truncated) != 1 {
		t.Fatalf("expected 2 stubs within MaxPages=2 and one truncated query: %+v", rep)
	}
	if gh.batches != 1 || !slices.ContainsFunc(gh.calls, func(c string) bool { return strings.Contains(c, "author:ana") && strings.HasSuffix(c, "|p2") }) {
		t.Fatalf("first pages in %d batches; second page not requested with the cursor: %v", gh.batches, gh.calls)
	}
}

func TestFastSweepFollowsAStoredPullRequestByItsStub(t *testing.T) {
	known, other := snapshot(), snapshot()
	other.Number = 8
	gh := &fakeGH{remaining: 4000, byRef: map[api.Ref]api.Snapshot{known.Ref: known, other.Ref: other}}
	c := newCollector(t, gh)
	ctx := context.Background()
	if _, err := c.Fast(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.Store.UpsertPR(ctx, pr.PR{Ref: known.Ref, State: "OPEN", Reasons: []string{"review-requested"}}); err != nil {
		t.Fatal(err)
	}
	gh.calls, gh.stubRefs = nil, nil
	rep, err := c.Fast(ctx)
	if err != nil || rep.Stubs != 1 || rep.Fetched != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
	if len(gh.stubRefs) != 1 || !slices.Equal(gh.stubRefs[0], []api.Ref{known.Ref}) {
		t.Fatalf("stub reads %v", gh.stubRefs)
	}
	if _, found, _ := c.Store.PR(ctx, other.Ref); found {
		t.Fatal("a pull request nothing follows entered the inventory")
	}
	if slices.ContainsFunc(gh.calls, func(c string) bool { return strings.Contains(c, "reviewed-by:") }) {
		t.Fatalf("a reviewed-by search: %v", gh.calls)
	}
}

func TestFastSweepContinuesAfterAQueryError(t *testing.T) {
	s := snapshot()
	gh := &fakeGH{remaining: 4000, failing: "team-review-requested", pages: map[string][]api.StubPage{"repo:": {{Stubs: []api.Stub{stubOf(s)}}}}, byRef: map[api.Ref]api.Snapshot{s.Ref: s}}
	c := newCollector(t, gh)
	cfg := testConfig()
	cfg.Scope.Repos = []string{"acme/api"}
	c.Cfg = configured(cfg)
	rep, err := c.Fast(context.Background())
	if err != nil || rep.Fetched != 1 || len(rep.Warnings) != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
}

func TestFastSweepSkipsUntilReset(t *testing.T) {
	gh := &fakeGH{remaining: 100, resetAt: now.Add(30 * time.Minute)}
	c := newCollector(t, gh)
	ctx := context.Background()
	rep, err := c.Fast(ctx)
	if err != nil || rep.Skipped == "" {
		t.Fatalf("expected a skipped sweep: %+v %v", rep, err)
	}
	if len(gh.calls) != 0 {
		t.Fatalf("a skipped sweep read GitHub: %v", gh.calls)
	}
	if at, _ := c.Store.Meta(ctx, "fast_sweep_at"); at != "" {
		t.Fatalf("a skipped sweep moved the watermark to %s", at)
	}
	later := func() time.Time { return now.Add(31 * time.Minute) }
	c.Now, gh.now = later, later
	rep, _ = c.Fast(ctx)
	if rep.Skipped != "" || len(gh.calls) == 0 {
		t.Fatalf("sweep did not resume after reset: %+v %v", rep, gh.calls)
	}
	if strings.Contains(gh.calls[0], "updated:") {
		t.Fatalf("the resumed sweep dropped the skipped window: %s", gh.calls[0])
	}
}

func TestFastSweepFailsWhenEveryQueryFails(t *testing.T) {
	ctx := context.Background()
	gh := &fakeGH{remaining: 4000, failing: "is:pr"}
	c := newCollector(t, gh)
	rep, err := c.Fast(ctx)
	if err == nil || rep.Failed != rep.Queries {
		t.Fatalf("expected an error when nothing answered: %+v %v", rep, err)
	}
	if at, _ := c.Store.Meta(ctx, "fast_sweep_at"); at != "" {
		t.Fatalf("a failed sweep moved the watermark to %s", at)
	}
}

func TestRefetchWithoutChangesAddsNoEvents(t *testing.T) {
	ctx := context.Background()
	s := snapshot()
	gh := &fakeGH{remaining: 4000}
	c := newCollector(t, gh)
	refresh(t, c, gh, s, now)
	before, _ := eventsOf(ctx, c, s.Ref)
	s.UpdatedAt = s.UpdatedAt.Add(time.Hour)
	refresh(t, c, gh, s, now.Add(time.Hour))
	after, _ := eventsOf(ctx, c, s.Ref)
	if gh.byRefCalls != 2 || len(after) != len(before) {
		t.Fatalf("refetch of an unchanged PR: %d by-ref calls, %d events then %d", gh.byRefCalls, len(before), len(after))
	}
}

func TestSlowSweepReadsOnlyWhatMoved(t *testing.T) {
	ctx := context.Background()
	s := snapshot()
	gh := &fakeGH{remaining: 4000, honorSince: true, pages: map[string][]api.StubPage{"is:pr": {{Stubs: []api.Stub{stubOf(s)}}}}, byRef: map[api.Ref]api.Snapshot{s.Ref: s}}
	c := newCollector(t, gh)
	if _, err := c.Fast(ctx); err != nil {
		t.Fatal(err)
	}
	later := now.Add(30 * time.Minute)
	c.Now = func() time.Time { return later }
	gh.served = nil
	if _, err := c.Fast(ctx); err != nil {
		t.Fatal(err)
	}
	gh.served = nil
	rep, err := c.Slow(ctx)
	if err != nil || gh.byRefCalls != 1 || rep.Fetched != 0 {
		t.Fatalf("an untouched open PR was read again: %+v %v (%d by-ref calls)", rep, err, gh.byRefCalls)
	}
	if pr, _, _ := c.Store.PR(ctx, s.Ref); !pr.LastSeenAt.Equal(later) {
		t.Fatalf("the slow sweep did not mark it seen: %v", pr.LastSeenAt)
	}
}

func TestSlowSweepRecoversWhatFastSweepsMissed(t *testing.T) {
	ctx := context.Background()
	s := snapshot()
	s.PullRequest.ReviewRequests = []string{"team:acme/search"}
	s.Author = "stranger"
	gh := &fakeGH{remaining: 4000, failing: "team-review-requested", pages: map[string][]api.StubPage{"team-review-requested": {{Stubs: []api.Stub{stubOf(s)}}}}, byRef: map[api.Ref]api.Snapshot{s.Ref: s}}
	c := newCollector(t, gh)
	if _, err := c.Fast(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := c.Store.PR(ctx, s.Ref); ok {
		t.Fatal("the failing query returned a PR")
	}
	gh.failing = ""
	if _, err := c.Slow(ctx); err != nil {
		t.Fatal(err)
	}
	pr, ok, _ := c.Store.PR(ctx, s.Ref)
	if !ok || !slices.Equal(pr.Reasons, []string{"team-review-requested"}) {
		t.Fatalf("the missed window was not recovered: %+v", pr)
	}
}

func TestSlowSweepKeepsScopeWhenATeamFails(t *testing.T) {
	ctx := context.Background()
	gh := &fakeGH{remaining: 4000}
	c := newCollector(t, gh)
	byDylan := api.Ref{Owner: "acme", Repo: "x", Number: 1}
	c.Store.UpsertPR(ctx, pr.PR{Ref: byDylan, Author: "dylan", State: "OPEN", LastSeenAt: now})
	c.Store.SetMeta(ctx, "fast_sweep_at", now.Format(time.RFC3339))
	if _, err := c.Slow(ctx); err != nil {
		t.Fatal(err)
	}
	gh.teamErr = errors.New("502 bad gateway")
	gh.served = nil
	rep, err := c.Slow(ctx)
	if err != nil || len(rep.Warnings) == 0 {
		t.Fatalf("%+v %v", rep, err)
	}
	if pr, _, _ := c.Store.PR(ctx, byDylan); !slices.Equal(pr.Reasons, []string{"author"}) {
		t.Fatalf("a transient team failure hid the PR: %v", pr.Reasons)
	}
	if raw, _ := c.Store.Meta(ctx, "scope_resolved"); !strings.Contains(raw, "acme/search") || !strings.Contains(raw, "dylan") {
		t.Fatalf("the stored scope lost the team: %s", raw)
	}
}

func TestSlowSweepKeepsDiscoveredReposWhenDiscoveryFails(t *testing.T) {
	ctx := context.Background()
	gh := &fakeGH{remaining: 4000, pages: map[string][]api.StubPage{"is:pr archived": {{Stubs: []api.Stub{{Ref: api.Ref{Owner: "acme", Repo: "web", Number: 3}, Author: "ana", NameWithOwner: "acme/web"}}}}}}
	c := newCollector(t, gh)
	inWeb := api.Ref{Owner: "acme", Repo: "web", Number: 9}
	c.Store.UpsertPR(ctx, pr.PR{Ref: inWeb, Author: "stranger", State: "OPEN", LastSeenAt: now})
	c.Store.SetMeta(ctx, "fast_sweep_at", now.Format(time.RFC3339))
	if _, err := c.Slow(ctx); err != nil {
		t.Fatal(err)
	}
	gh.failing, gh.served = "is:pr archived", nil
	if _, err := c.Slow(ctx); err != nil {
		t.Fatal(err)
	}
	if pr, _, _ := c.Store.PR(ctx, inWeb); !slices.Equal(pr.Reasons, []string{"repo"}) {
		t.Fatalf("a failed discovery hid the PR: %v", pr.Reasons)
	}
}

func TestSlowSweepDiscoversReposAndRefreshesUnseen(t *testing.T) {
	s := snapshot()
	gh := &fakeGH{remaining: 4000, pages: map[string][]api.StubPage{"author:ana": {{Stubs: []api.Stub{{Ref: api.Ref{Owner: "acme", Repo: "web", Number: 3}, Author: "ana", NameWithOwner: "acme/web"}}}}}, byRef: map[api.Ref]api.Snapshot{s.Ref: s}}
	c := newCollector(t, gh)
	ctx := context.Background()
	c.Store.UpsertPR(ctx, pr.PR{Ref: s.Ref, Author: "ana", State: "OPEN", Reasons: []string{"author"}, LastSeenAt: now.Add(-3 * time.Hour)})
	c.Store.SetMeta(ctx, "fast_sweep_at", now.Add(-time.Hour).Format(time.RFC3339))
	rep, err := c.Slow(ctx)
	if err != nil || rep.Fetched < 1 {
		t.Fatalf("%+v %v", rep, err)
	}
	scopeJSON, _ := c.Store.Meta(ctx, "scope_resolved")
	if !strings.Contains(scopeJSON, "acme/web") {
		t.Fatalf("discovered repo not saved: %s", scopeJSON)
	}
}

func TestSlowSweepClearsReasonsOutOfScope(t *testing.T) {
	gh := &fakeGH{remaining: 4000}
	c := newCollector(t, gh)
	ctx := context.Background()
	out := api.Ref{Owner: "other", Repo: "x", Number: 1}
	c.Store.UpsertPR(ctx, pr.PR{Ref: out, Author: "stranger", State: "OPEN", Reasons: []string{"repo"}, LastSeenAt: now})
	c.Store.SetMeta(ctx, "fast_sweep_at", now.Format(time.RFC3339))
	c.Slow(ctx)
	pr, _, _ := c.Store.PR(ctx, out)
	if len(pr.Reasons) != 0 {
		t.Fatalf("still in scope: %v", pr.Reasons)
	}
}

func TestFastSweepRunsWhenRateLimitUnknown(t *testing.T) {
	ctx := context.Background()
	gh := &fakeGH{remaining: 0, resetAt: now.Add(30 * time.Minute), noLimit: true}
	c := newCollector(t, gh)
	rep, err := c.Fast(ctx)
	if err != nil || rep.Skipped != "" || len(gh.calls) == 0 {
		t.Fatalf("an unknown limit must not skip: %+v %v %v", rep, err, gh.calls)
	}
	if b, _ := StoredBudget(ctx, c.Store); b != nil || rep.RateLimit.Limit != 0 {
		t.Fatalf("an unknown limit was kept: stored %+v, report %+v", b, rep.RateLimit)
	}
}

func refresh(t *testing.T, c *Collector, gh *fakeGH, s api.Snapshot, at time.Time) {
	t.Helper()
	gh.pages = map[string][]api.StubPage{"author:ana": {{Stubs: []api.Stub{stubOf(s)}}}}
	gh.served = nil
	gh.byRef = map[api.Ref]api.Snapshot{s.Ref: s}
	c.Now = func() time.Time { return at }
	if _, err := c.Fast(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestDismissedChangeRequestIsNotDecisive(t *testing.T) {
	ctx := context.Background()
	s := snapshot()
	s.Reviews = []pr.Review{{ID: "r1", State: pr.ReviewChangesRequested, Author: "carla", SubmittedAt: s.UpdatedAt.Add(-time.Hour), CommitOid: "h2"}}
	gh := &fakeGH{remaining: 4000}
	c := newCollector(t, gh)
	reviews := func() []string {
		evs, _ := eventsOf(ctx, c, s.Ref)
		var out []string
		for _, e := range evs {
			if e.Kind == pr.KindReview {
				out = append(out, e.Key+" "+e.Data)
			}
		}
		return out
	}
	refresh(t, c, gh, s, now)
	if got := reviews(); !slices.Equal(got, []string{"r1 CHANGES_REQUESTED:h2"}) {
		t.Fatalf("before dismissal: %v", got)
	}
	s.Reviews[0].State = pr.ReviewDismissed
	s.UpdatedAt = s.UpdatedAt.Add(time.Hour)
	refresh(t, c, gh, s, now.Add(time.Hour))
	if got := reviews(); !slices.Equal(got, []string{"r1 CHANGES_REQUESTED:h2", "r1:DISMISSED DISMISSED:h2"}) {
		t.Fatalf("a dismissed change request still waits on the author: %v", got)
	}
}

func TestPushDatedBeforePreviousHeadIsClampedToFirstSighting(t *testing.T) {
	ctx := context.Background()
	s := snapshot()
	gh := &fakeGH{remaining: 4000}
	c := newCollector(t, gh)
	refresh(t, c, gh, s, now)
	seen := now.Add(time.Hour)
	s.HeadRefOid = "h3"
	s.CommitsDated = append(s.CommitsDated, api.DatedCommit{Oid: "h3", Parents: 1, At: s.CommitsDated[0].At.Add(-time.Hour)})
	s.UpdatedAt = seen
	refresh(t, c, gh, s, seen)
	evs, _ := eventsOf(ctx, c, s.Ref)
	var pushes []pr.Event
	for _, e := range evs {
		if e.Kind == pr.KindPush {
			pushes = append(pushes, e)
		}
	}
	last := pushes[len(pushes)-1]
	if last.Key != "h3" || !last.At.Equal(seen) {
		t.Fatalf("new head must be the latest push, at its first sighting: %+v", pushes)
	}
	for _, e := range pushes {
		if e.Key == "h2" && !e.At.Equal(s.CommitsDated[2].At) {
			t.Fatalf("an in-order push keeps its commit date: %+v", e)
		}
	}
}

func TestSlowSweepStoresRecentlyClosed(t *testing.T) {
	ctx := context.Background()
	s := snapshot()
	s.State, s.MergedAt = "MERGED", s.UpdatedAt
	gh := &fakeGH{remaining: 4000, pages: map[string][]api.StubPage{"is:closed": {{Stubs: []api.Stub{stubOf(s)}}}}, byRef: map[api.Ref]api.Snapshot{s.Ref: s}}
	c := newCollector(t, gh)
	if _, err := c.Slow(ctx); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(gh.calls, func(q string) bool {
		return strings.Contains(q, "updated:>=2026-09-21T10:00:00Z") && strings.Contains(q, "is:closed")
	}) {
		t.Fatalf("no closed search over the last 14 days: %v", gh.calls)
	}
	pr, ok, _ := c.Store.PR(ctx, s.Ref)
	if !ok || pr.State != "MERGED" {
		t.Fatalf("%+v", pr)
	}
}

func TestSyncWorkspacesLinksByPRAndBranch(t *testing.T) {
	ctx := context.Background()
	a, b := snapshot(), snapshot()
	b.Number, b.HeadRefName = 8, "feat/export"
	gh := &fakeGH{remaining: 4000, pages: map[string][]api.StubPage{"author:ana": {{Stubs: []api.Stub{stubOf(a), stubOf(b)}}}}, byRef: map[api.Ref]api.Snapshot{a.Ref: a, b.Ref: b}}
	c := newCollector(t, gh)
	ws := acmeAPI(
		src.Workspace{ID: "w0", IsMain: true, Branch: "feat/export"},
		src.Workspace{ID: "w1", DisplayName: "REVIEW batch", Status: "in-review", Units: linkedTo(7), AgentStates: []string{"working"}},
		src.Workspace{ID: "w2", DisplayName: "export", Branch: "feat/export"},
	)
	ws.edit(func(l *src.Listing) {
		l.Workspaces = append(l.Workspaces, src.Workspace{ID: "w3", Repo: "acme/api", RepoID: "r1", DisplayName: "remote", Units: linkedTo(7)})
	})
	c.WS = ws
	if _, err := c.Fast(ctx); err != nil {
		t.Fatal(err)
	}
	pa, _, _ := c.Store.PR(ctx, a.Ref)
	pb, _, _ := c.Store.PR(ctx, b.Ref)
	if pa.Workspace.ID != "w1" || !pa.Workspace.IsReview || pa.Workspace.Status != "in-review" || !slices.Equal(pa.Workspace.AgentStates, []string{"working"}) {
		t.Fatalf("linked workspace: %+v", pa.Workspace)
	}
	if pb.Workspace.ID != "w2" || pb.Workspace.IsReview {
		t.Fatalf("branch workspace: %+v", pb.Workspace)
	}
}

func TestSyncWorkspacesLinksAMainCheckoutOnTheHeadBranch(t *testing.T) {
	for name, fork := range map[string]bool{"same repository": false, "fork": true} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			a, b := snapshot(), snapshot()
			a.HeadRefName, a.IsCrossRepository = "feat/creds", fork
			b.Number, b.HeadRefName = 8, "feat/other"
			gh := &fakeGH{remaining: 4000, pages: map[string][]api.StubPage{"author:ana": {{Stubs: []api.Stub{stubOf(a), stubOf(b)}}}}, byRef: map[api.Ref]api.Snapshot{a.Ref: a, b.Ref: b}}
			c := newCollector(t, gh)
			c.WS = acmeAPI(
				src.Workspace{ID: "main-api", RepoID: "r1", DisplayName: "api", IsMain: true, Branch: "feat/creds", Units: linkedTo(8)},
			)
			if _, err := c.Fast(ctx); err != nil {
				t.Fatal(err)
			}
			pa, _, _ := c.Store.PR(ctx, a.Ref)
			pb, _, _ := c.Store.PR(ctx, b.Ref)
			if linked := pa.Workspace.ID == "main-api"; linked == fork || pa.Workspace.IsReview {
				t.Fatalf("main checkout on the head branch, fork %v: %+v", fork, pa.Workspace)
			}
			if pb.Workspace.ID != "" {
				t.Fatalf("a main checkout belongs to a pull request by its branch, not by a link: %+v", pb.Workspace)
			}
		})
	}
}

func TestSyncWorkspacesNeverLinksAMainCheckoutOnTheDefaultBranch(t *testing.T) {
	for _, branch := range []string{"develop", "main", "master"} {
		t.Run(branch, func(t *testing.T) {
			ctx := context.Background()
			a := snapshot()
			a.HeadRefName, a.BaseRefName = branch, "production"
			gh := &fakeGH{remaining: 4000, pages: map[string][]api.StubPage{"author:ana": {{Stubs: []api.Stub{stubOf(a)}}}}, byRef: map[api.Ref]api.Snapshot{a.Ref: a}}
			c := newCollector(t, gh)
			ws := acmeAPI(src.Workspace{ID: "main-api", DisplayName: "api", IsMain: true, Branch: branch})
			ws.edit(func(l *src.Listing) { l.Repos[0].DefaultBranch = "develop" })
			c.WS = ws
			if _, err := c.Fast(ctx); err != nil {
				t.Fatal(err)
			}
			if pa, _, _ := c.Store.PR(ctx, a.Ref); pa.Workspace.ID != "" {
				t.Fatalf("a release PR took the main checkout on %s: %+v", branch, pa.Workspace)
			}
		})
	}
}

func TestSyncWorkspacesPrefersAWorktreeOverTheMainCheckout(t *testing.T) {
	ctx := context.Background()
	a := snapshot()
	a.HeadRefName = "feat/creds"
	gh := &fakeGH{remaining: 4000, pages: map[string][]api.StubPage{"author:ana": {{Stubs: []api.Stub{stubOf(a)}}}}, byRef: map[api.Ref]api.Snapshot{a.Ref: a}}
	c := newCollector(t, gh)
	main := src.Workspace{ID: "main-api", RepoID: "r1", DisplayName: c.Cfg().ReviewPrefix + "api", IsMain: true, Branch: "feat/creds"}
	worktree := src.Workspace{ID: "wt", RepoID: "r1", DisplayName: "creds", Units: linkedTo(7)}
	for _, list := range [][]src.Workspace{{main, worktree}, {worktree, main}} {
		c.WS = acmeAPI(list...)
		if _, err := c.Fast(ctx); err != nil {
			t.Fatal(err)
		}
		if pa, _, _ := c.Store.PR(ctx, a.Ref); pa.Workspace.ID != "wt" {
			t.Fatalf("order %s first: %+v", list[0].ID, pa.Workspace)
		}
	}
	c.WS = acmeAPI(main)
	if _, err := c.Fast(ctx); err != nil {
		t.Fatal(err)
	}
	if pa, _, _ := c.Store.PR(ctx, a.Ref); pa.Workspace.ID != "main-api" || pa.Workspace.IsReview {
		t.Fatalf("a main checkout is an author workspace whatever its name: %+v", pa.Workspace)
	}
}

func TestSyncWorkspacesMatchesAMainCheckoutByItsOwnBranch(t *testing.T) {
	ctx := context.Background()
	a := snapshot()
	a.HeadRefName = "feat/creds"
	gh := &fakeGH{remaining: 4000, pages: map[string][]api.StubPage{"author:ana": {{Stubs: []api.Stub{stubOf(a)}}}}, byRef: map[api.Ref]api.Snapshot{a.Ref: a}}
	c := newCollector(t, gh)
	c.WS = acmeAPI(src.Workspace{ID: "main-api", RepoID: "r1", IsMain: true, Branch: "wip", PushBranch: "feat/creds"})
	if _, err := c.Fast(ctx); err != nil {
		t.Fatal(err)
	}
	if pa, _, _ := c.Store.PR(ctx, a.Ref); pa.Workspace.ID != "" {
		t.Fatalf("linked a main checkout by its push branch: %+v", pa.Workspace)
	}
}

func TestSyncWorkspacesLinksWithoutAgentActivity(t *testing.T) {
	ctx := context.Background()
	a := snapshot()
	gh := &fakeGH{remaining: 4000, pages: map[string][]api.StubPage{"author:ana": {{Stubs: []api.Stub{stubOf(a)}}}}, byRef: map[api.Ref]api.Snapshot{a.Ref: a}}
	c := newCollector(t, gh)
	ws := acmeAPI(src.Workspace{ID: "w1", Units: linkedTo(7)})
	c.WS = ws
	rep, err := c.Fast(ctx)
	if err != nil || len(rep.Warnings) != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
	if pa, _, _ := c.Store.PR(ctx, a.Ref); pa.Workspace.ID != "w1" || pa.Workspace.AgentStates != nil {
		t.Fatalf("workspace not linked: %+v", pa.Workspace)
	}
	ws.edit(func(l *src.Listing) { l.Partial, l.Workspaces = true, nil })
	if rep, err = c.Fast(ctx); err != nil || len(rep.Warnings) != 1 {
		t.Fatalf("a partial listing: %+v %v", rep, err)
	}
	if pa, _, _ := c.Store.PR(ctx, a.Ref); pa.Workspace.ID != "w1" {
		t.Fatalf("a partial listing unlinked the workspace: %+v", pa.Workspace)
	}
}

func TestSlowSweepSkipsUntilReset(t *testing.T) {
	gh := &fakeGH{remaining: 100, resetAt: now.Add(30 * time.Minute)}
	c := newCollector(t, gh)
	ctx := context.Background()
	rep, err := c.Slow(ctx)
	if err != nil || rep.Skipped == "" {
		t.Fatalf("expected a skipped sweep: %+v %v", rep, err)
	}
	if len(gh.calls) != 0 {
		t.Fatalf("a skipped sweep read GitHub: %v", gh.calls)
	}
	if at, _ := c.Store.Meta(ctx, "slow_sweep_at"); at != "" {
		t.Fatalf("a skipped sweep moved the watermark to %s", at)
	}
	later := func() time.Time { return now.Add(31 * time.Minute) }
	c.Now, gh.now = later, later
	if rep, _ = c.Slow(ctx); rep.Skipped != "" || len(gh.calls) == 0 {
		t.Fatalf("sweep did not resume after reset: %+v %v", rep, gh.calls)
	}
}

func TestStoredBudgetReadsWhatTheSweepSaved(t *testing.T) {
	gh := &fakeGH{remaining: 4321, resetAt: now.Add(time.Hour).UTC()}
	c := newCollector(t, gh)
	ctx := context.Background()
	if b, err := StoredBudget(ctx, c.Store); err != nil || b != nil {
		t.Fatalf("empty store: %+v %v", b, err)
	}
	rep, err := c.Fast(ctx)
	if err != nil || rep.RateLimit.Remaining != 4321 {
		t.Fatalf("%+v %v", rep, err)
	}
	want := []api.Limit{gh.Budget()[api.ResourceGraphQL]}
	if b, err := StoredBudget(ctx, c.Store); err != nil || !slices.Equal(b, want) {
		t.Fatalf("got %+v %v, want %+v", b, err, want)
	}
}

func TestARefusalMidRunSkipsAndKeepsTheBudget(t *testing.T) {
	refused := &api.BudgetError{Priority: api.PriorityBackground, Limit: api.Limit{Resource: api.ResourceGraphQL, Limit: 5000, Remaining: 2400, ResetAt: now.Add(time.Hour)}}
	for name, set := range map[string]func(*fakeGH){
		"search": func(f *fakeGH) { f.searchErr = refused },
		"read":   func(f *fakeGH) { f.byRefErr = refused },
	} {
		t.Run(name, func(t *testing.T) {
			s := snapshot()
			gh := &fakeGH{remaining: 2600, resetAt: now.Add(time.Hour), pages: map[string][]api.StubPage{"author:ana": {{Stubs: []api.Stub{stubOf(s)}}}}, byRef: map[api.Ref]api.Snapshot{s.Ref: s}}
			c := newCollector(t, gh)
			set(gh)
			ctx := context.Background()
			for _, run := range []func(context.Context) (Report, error){c.Fast, c.Slow} {
				rep, err := run(ctx)
				if err != nil || rep.Skipped == "" {
					t.Fatalf("a refusal mid-run: %+v %v", rep, err)
				}
			}
			for _, key := range []string{"fast_sweep_at", "slow_sweep_at"} {
				if at, _ := c.Store.Meta(ctx, key); at != "" {
					t.Fatalf("a cut-short sweep moved %s to %s", key, at)
				}
			}
			if b, _ := StoredBudget(ctx, c.Store); len(b) != 1 || b[0].Remaining != 2600 {
				t.Fatalf("budget kept %+v", b)
			}
		})
	}
}

func TestFastPersistsBudgetOnce(t *testing.T) {
	gh := &fakeGH{remaining: 4321, resetAt: now.Add(time.Hour)}
	c := newCollector(t, gh)
	if _, err := c.Fast(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := c.Fast(ctx); err != nil {
		t.Fatal(err)
	}
	if b, err := StoredBudget(ctx, c.Store); err != nil || len(b) != 1 || b[0].Remaining != 4321 {
		t.Fatalf("budget kept %+v: %v", b, err)
	}
}

func TestFastSweepRereadsWhenTheHeadChecksChange(t *testing.T) {
	ctx := context.Background()
	s := snapshot()
	s.ChecksState = "PENDING"
	gh := &fakeGH{remaining: 4000, pages: map[string][]api.StubPage{"author:ana": {{Stubs: []api.Stub{stubOf(s)}}}}, byRef: map[api.Ref]api.Snapshot{s.Ref: s}}
	c := newCollector(t, gh)
	if _, err := c.Fast(ctx); err != nil {
		t.Fatal(err)
	}
	s.ChecksState = "FAILURE"
	s.Checks = []pr.Check{
		{Name: "Build and test", Failed: true, URL: "https://github.com/acme/api/actions/runs/1/job/2", Summary: "exit\ncode 1"},
		{Name: "lint"},
		{Name: "Preview site", Failed: true, URL: "javascript:alert(1)"},
		{Name: "Cloud Build", Failed: true, Summary: "Summary"},
	}
	gh.byRef[s.Ref] = s
	gh.pages["author:ana"] = []api.StubPage{{Stubs: []api.Stub{stubOf(s)}}}
	gh.served = nil
	rep, err := c.Fast(ctx)
	if err != nil || rep.Fetched != 1 {
		t.Fatalf("checks concluded without an update and the PR was not read: %+v %v", rep, err)
	}
	got, _, _ := c.Store.PR(ctx, s.Ref)
	if got.ChecksState != "FAILURE" {
		t.Fatalf("checks state: %q", got.ChecksState)
	}
	want := []pr.FailingCheck{
		{Name: "Build and test", URL: "https://github.com/acme/api/actions/runs/1/job/2", Summary: "exit code 1"},
		{Name: "Preview site"},
		{Name: "Cloud Build"},
	}
	if !slices.Equal(got.FailingChecks, want) {
		t.Fatalf("failing checks %+v", got.FailingChecks)
	}
}

func TestFastSweepRereadsWhenTheFailedChecksChange(t *testing.T) {
	ctx := context.Background()
	s := snapshot()
	s.ChecksState = "FAILURE"
	s.Checks = []pr.Check{{Name: "lint", Failed: true}, {Name: "e2e\nsuite"}}
	gh := &fakeGH{remaining: 4000, pages: map[string][]api.StubPage{"author:ana": {{Stubs: []api.Stub{stubOf(s)}}}}, byRef: map[api.Ref]api.Snapshot{s.Ref: s}}
	c := newCollector(t, gh)
	sweep := func(want int) {
		t.Helper()
		gh.byRef[s.Ref] = s
		gh.pages["author:ana"] = []api.StubPage{{Stubs: []api.Stub{stubOf(s)}}}
		if rep, err := c.Fast(ctx); err != nil || rep.Fetched != want {
			t.Fatalf("fetched %d, want %d: %v", rep.Fetched, want, err)
		}
	}
	sweep(1)
	s.Checks[1].Failed = true
	sweep(1)
	if pr, _, _ := c.Store.PR(ctx, s.Ref); len(pr.FailingChecks) != 2 || pr.FailingChecks[1].Name != "e2e suite" {
		t.Fatalf("failing checks %+v", pr.FailingChecks)
	}
	sweep(0)
}

func TestAMergeableChangeIsWrittenWithoutAFetch(t *testing.T) {
	ctx := context.Background()
	s := snapshot()
	gh := &fakeGH{remaining: 4000, pages: map[string][]api.StubPage{"author:ana": {{Stubs: []api.Stub{stubOf(s)}}}}, byRef: map[api.Ref]api.Snapshot{s.Ref: s}}
	c := newCollector(t, gh)
	sweep := func(stub string, want int) {
		t.Helper()
		st := stubOf(s)
		st.Mergeable = stub
		gh.byRef[s.Ref] = s
		gh.pages["author:ana"] = []api.StubPage{{Stubs: []api.Stub{st}}}
		gh.served = nil
		if rep, err := c.Fast(ctx); err != nil || rep.Fetched != want {
			t.Fatalf("stub %s: fetched %d, want %d: %v", stub, rep.Fetched, want, err)
		}
	}
	stored := func() string {
		pr, _, _ := c.Store.PR(ctx, s.Ref)
		return pr.Mergeable
	}
	sweep("", 1)
	if stored() != "" {
		t.Fatalf("stored %q", stored())
	}
	sweep("MERGEABLE", 0)
	if stored() != "MERGEABLE" {
		t.Fatalf("a known stub over an empty value: %q", stored())
	}
	sweep("CONFLICTING", 0)
	if stored() != "CONFLICTING" {
		t.Fatalf("stored %q", stored())
	}
	s.Mergeable = "UNKNOWN"
	s.UpdatedAt = s.UpdatedAt.Add(time.Minute)
	sweep("UNKNOWN", 1)
	if stored() != "CONFLICTING" {
		t.Fatalf("a full read answering UNKNOWN lost the conflict: %q", stored())
	}
}

func TestSlowSweepBesideALaterFastSweepReadsOnlyWhatMoved(t *testing.T) {
	ctx := context.Background()
	s := snapshot()
	gh := &fakeGH{remaining: 4000, pages: map[string][]api.StubPage{"is:pr": {{Stubs: []api.Stub{stubOf(s)}}}}, byRef: map[api.Ref]api.Snapshot{s.Ref: s}}
	c := newCollector(t, gh)
	if _, err := c.Fast(ctx); err != nil {
		t.Fatal(err)
	}
	c.Store.SetMeta(ctx, "fast_sweep_at", now.Add(time.Minute).Format(time.RFC3339))
	gh.served = nil
	rep, err := c.Slow(ctx)
	if err != nil || gh.byRefCalls != 1 || rep.Fetched != 0 {
		t.Fatalf("an untouched open PR was read again: %+v %v (%d by-ref calls)", rep, err, gh.byRefCalls)
	}
}

func TestKeepPreviousRestoresMembershipOfAFailedTeam(t *testing.T) {
	prev := scope.Resolved{Teams: []string{"acme/platform", "acme/search"}, MyTeams: []string{"acme/search"}}
	cur := scope.Resolved{Teams: []string{"acme/platform"}}
	got := keepPrevious(cur, prev, []string{"acme/platform", "acme/search"}, false)
	if !slices.Equal(got.MyTeams, []string{"acme/search"}) {
		t.Fatalf("my teams: %v", got.MyTeams)
	}
	prev.Viewer = "me"
	got = keepPrevious(scope.Resolved{Teams: []string{"acme/platform", "acme/search"}}, prev, nil, false)
	if got.Viewer != "me" || !slices.Equal(got.MyTeams, []string{"acme/search"}) {
		t.Fatalf("viewer unknown: %+v", got)
	}
}

func TestMyTeamsReadsTheStoredScope(t *testing.T) {
	ctx := context.Background()
	c := newCollector(t, &fakeGH{remaining: 4000})
	if got, err := MyTeams(ctx, c.Store); err != nil || got != nil {
		t.Fatalf("no scope yet: %v %v", got, err)
	}
	c.Store.SetMeta(ctx, "scope_resolved", `{"Teams":["acme/platform","acme/search"],"MyTeams":["acme/search"]}`)
	if got, err := MyTeams(ctx, c.Store); err != nil || !slices.Equal(got, []string{"acme/search"}) {
		t.Fatalf("%v %v", got, err)
	}
}

func statusData(t *testing.T, c *Collector, ref api.Ref) []string {
	t.Helper()
	evs, err := eventsOf(context.Background(), c, ref)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range evs {
		if e.Kind == pr.KindStatus {
			out = append(out, e.Data)
		}
	}
	return out
}

func TestSyncWorkspacesRecordsCardsEnteringAndLeavingNeedsMe(t *testing.T) {
	ctx := context.Background()
	a := snapshot()
	gh := &fakeGH{remaining: 4000, pages: map[string][]api.StubPage{"author:ana": {{Stubs: []api.Stub{stubOf(a)}}}}, byRef: map[api.Ref]api.Snapshot{a.Ref: a}}
	c := newCollector(t, gh)
	ws := acmeAPI(src.Workspace{ID: "w1", DisplayName: "feat", Status: "in-review", Units: linkedTo(7)})
	c.WS = ws
	clock := now
	c.Now = func() time.Time { return clock }
	sync := func(status string) {
		t.Helper()
		clock = clock.Add(2 * time.Minute)
		ws.edit(func(l *src.Listing) { l.Workspaces[0].Status = status })
		if _, err := c.Fast(ctx); err != nil {
			t.Fatal(err)
		}
	}
	sync("in-review")
	sync("in-progress")
	if got := statusData(t, c, a.Ref); len(got) != 0 {
		t.Fatalf("recorded columns outside needs-me: %v", got)
	}
	sync("needs-me")
	sync("needs-me")
	if got := statusData(t, c, a.Ref); !slices.Equal(got, []string{"needs-me"}) {
		t.Fatalf("parked by the skill: %v", got)
	}
	if err := c.Store.RecordEvents(ctx, []pr.Event{{PR: a.Ref, Kind: pr.KindStatus, At: c.Now().Add(time.Minute), Data: "in-progress"}}); err != nil {
		t.Fatal(err)
	}
	sync("in-progress")
	if got := statusData(t, c, a.Ref); !slices.Equal(got, []string{"needs-me", "in-progress"}) {
		t.Fatalf("a move the sweep recorded was recorded again: %v", got)
	}
	sync("needs-me")
	if got := statusData(t, c, a.Ref); !slices.Equal(got, []string{"needs-me", "in-progress", "needs-me"}) {
		t.Fatalf("parked again: %v", got)
	}
}

func outOfScope(n int, author, state string) api.Snapshot {
	s := snapshot()
	s.Ref.Number, s.Author, s.State, s.URL = n, author, state, ""
	s.PullRequest.ReviewRequests, s.ReviewRequests, s.Reviews = nil, nil, nil
	return s
}

func TestLoopWorkspacesAreASource(t *testing.T) {
	ctx := context.Background()
	held, merged, mine := outOfScope(444, "bruno", "OPEN"), outOfScope(445, "bruno", "MERGED"), outOfScope(446, "carla", "OPEN")
	gh := &fakeGH{remaining: 4000, byRef: map[api.Ref]api.Snapshot{held.Ref: held, merged.Ref: merged, mine.Ref: mine}}
	c := newCollector(t, gh)
	c.WS = acmeAPI(
		src.Workspace{ID: "w1", RepoID: "r1", DisplayName: "REVIEW fix/billing", Status: "needs-me", Units: linkedTo(444)},
		src.Workspace{ID: "w2", RepoID: "r1", DisplayName: "REVIEW old", Status: "completed", Units: linkedTo(445)},
		src.Workspace{ID: "w3", RepoID: "r1", DisplayName: "takeover", Status: "in-progress", Units: linkedTo(446)},
	)
	if _, err := c.Fast(ctx); err != nil {
		t.Fatal(err)
	}
	for ref, want := range map[api.Ref]struct {
		ws     string
		review bool
		state  string
	}{held.Ref: {"w1", true, "OPEN"}, merged.Ref: {"w2", true, "MERGED"}, mine.Ref: {"w3", false, "OPEN"}} {
		pr, ok, err := c.Store.PR(ctx, ref)
		if err != nil || !ok || pr.Workspace.ID != want.ws || pr.Workspace.IsReview != want.review || pr.State != want.state {
			t.Fatalf("%s: %+v %v %v", ref, pr, ok, err)
		}
		if len(pr.Reasons) != 0 {
			t.Fatalf("%s: a workspace is not a scope reason: %v", ref, pr.Reasons)
		}
	}
	rep, err := c.Fast(ctx)
	if err != nil || rep.Fetched != 0 || len(gh.stubRefs) != 1 || len(gh.stubRefs[0]) != 2 {
		t.Fatalf("out-of-scope open PRs of the loop not stub-checked: %+v %v %v", rep, err, gh.stubRefs)
	}
	mine.UpdatedAt = mine.UpdatedAt.Add(time.Minute)
	gh.byRef[mine.Ref] = mine
	if rep, err = c.Fast(ctx); err != nil || rep.Fetched != 1 {
		t.Fatalf("a moved out-of-scope PR of the loop not read: %+v %v", rep, err)
	}
}

func TestSlowSweepFindsTheUnlinkedWorkspaceByBranch(t *testing.T) {
	ctx := context.Background()
	s := outOfScope(447, "bruno", "OPEN")
	s.HeadRefName = "fix/ack"
	ack := api.BranchQuery{Owner: "acme", Repo: "api", Branch: "fix/ack"}
	two := api.BranchQuery{Owner: "acme", Repo: "api", Branch: "fix/two"}
	gh := &fakeGH{remaining: 4000, byRef: map[api.Ref]api.Snapshot{s.Ref: s, outOfScope(448, "x", "OPEN").Ref: outOfScope(448, "x", "OPEN")},
		byBranch: map[api.BranchQuery][]api.BranchMatch{
			ack: {{Number: 447, State: "OPEN"}},
			two: {{Number: 448, State: "OPEN"}, {Number: 449, State: "OPEN"}},
		}}
	c := newCollector(t, gh)
	c.WS = acmeAPI(
		src.Workspace{ID: "w1", RepoID: "r1", DisplayName: "REVIEW fix/ack", Branch: "fix/ack"},
		src.Workspace{ID: "w2", RepoID: "r1", DisplayName: "REVIEW fix/two", Branch: "fix/two"},
	)
	if _, err := c.Slow(ctx); err != nil {
		t.Fatal(err)
	}
	if len(gh.branchQs) != 2 || slices.ContainsFunc(gh.calls, func(q string) bool { return strings.Contains(q, "head:") }) {
		t.Fatalf("branch queries %v, searches %q", gh.branchQs, gh.calls)
	}
	if _, ok, _ := c.Store.PR(ctx, outOfScope(448, "x", "OPEN").Ref); ok {
		t.Fatal("an ambiguous branch imported a pull request")
	}
	if _, err := c.Fast(ctx); err != nil {
		t.Fatal(err)
	}
	if pr, ok, _ := c.Store.PR(ctx, s.Ref); !ok || pr.Workspace.ID != "w1" {
		t.Fatalf("%+v %v", pr, ok)
	}
}

func TestAnUnreadableLinkedPRIsNotAskedForAgainUntilTheSlowSweep(t *testing.T) {
	ctx := context.Background()
	gh := &fakeGH{remaining: 4000}
	c := newCollector(t, gh)
	c.WS = acmeAPI(src.Workspace{ID: "w1", RepoID: "r1", DisplayName: "REVIEW gone", Units: linkedTo(999)})
	gone := api.Ref{Owner: "acme", Repo: "api", Number: 999}
	asked := func() int {
		n := 0
		for _, r := range gh.read {
			if r == gone {
				n++
			}
		}
		return n
	}
	for range 3 {
		if _, err := c.Fast(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if asked() != 1 {
		t.Fatalf("asked %d times", asked())
	}
	if _, err := c.Slow(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Fast(ctx); err != nil {
		t.Fatal(err)
	}
	if asked() != 2 {
		t.Fatalf("not asked again after the slow sweep: %d", asked())
	}
}

func TestAnUnreadableTrackedPRIsNotAskedForAgainUntilTheSlowSweep(t *testing.T) {
	ctx := context.Background()
	saml := api.Ref{Owner: "acme", Repo: "api", Number: 9}
	gh := &fakeGH{remaining: 4000, refErrs: map[api.Ref]error{saml: errors.New("acme/api#9: Resource protected by organization SAML enforcement")}}
	c := newCollector(t, gh)
	if err := c.Store.UpsertPR(ctx, pr.PR{Ref: saml, Author: "bruno", State: "OPEN", LastSeenAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := setTracked(ctx, c.Store, saml); err != nil {
		t.Fatal(err)
	}
	asked := func() int {
		n := len(slices.DeleteFunc(slices.Clone(gh.read), func(r api.Ref) bool { return r != saml }))
		for _, refs := range gh.stubRefs {
			if slices.Contains(refs, saml) {
				n++
			}
		}
		return n
	}
	for range 3 {
		if _, err := c.Fast(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if asked() != 1 {
		t.Fatalf("asked %d times", asked())
	}
	if _, err := c.Slow(ctx); err != nil {
		t.Fatal(err)
	}
	before := asked()
	if _, err := c.Fast(ctx); err != nil {
		t.Fatal(err)
	}
	if asked() != before+1 {
		t.Fatalf("not asked again after the slow sweep: %d then %d", before, asked())
	}
}

func TestFastSweepReadsAnAbsentWorkspaceLink(t *testing.T) {
	ctx := context.Background()
	a, held := snapshot(), outOfScope(444, "bruno", "OPEN")
	gh := &fakeGH{remaining: 4000, pages: map[string][]api.StubPage{"author:ana": {{Stubs: []api.Stub{stubOf(a)}}}},
		byRef: map[api.Ref]api.Snapshot{a.Ref: a, held.Ref: held}}
	c := newCollector(t, gh)
	c.WS = acmeAPI(src.Workspace{ID: "w1", RepoID: "r1", DisplayName: "REVIEW fix", Units: linkedTo(444)})
	if _, err := c.Fast(ctx); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(gh.read, []api.Ref{a.Ref, held.Ref}) {
		t.Fatalf("%d by-ref calls reading %v", gh.byRefCalls, gh.read)
	}
}

func TestSlowSweepDoesNotRefreshWhatNeitherScopeNorAWorkspaceHolds(t *testing.T) {
	ctx := context.Background()
	orphan := outOfScope(450, "bruno", "OPEN")
	gh := &fakeGH{remaining: 4000, byRef: map[api.Ref]api.Snapshot{orphan.Ref: orphan}}
	c := newCollector(t, gh)
	if err := c.Store.UpsertPR(ctx, pr.PR{Ref: orphan.Ref, Author: "bruno", State: "OPEN", LastSeenAt: now.Add(-48 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := c.Store.SetMeta(ctx, metaFastAt, now.Add(-time.Minute).Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Slow(ctx); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(gh.read, orphan.Ref) {
		t.Fatalf("re-read an open PR nothing follows: %v", gh.read)
	}
}

func TestSyncWorkspacesKeepsTheCardComment(t *testing.T) {
	ctx := context.Background()
	a := snapshot()
	gh := &fakeGH{remaining: 4000, pages: map[string][]api.StubPage{"author:ana": {{Stubs: []api.Stub{stubOf(a)}}}}, byRef: map[api.Ref]api.Snapshot{a.Ref: a}}
	c := newCollector(t, gh)
	c.WS = acmeAPI(src.Workspace{ID: "w1", RepoID: "r1", DisplayName: "feat", Status: "needs-me", Units: linkedTo(7),
		Comment: "triage asks:\nkeep the old column?"})
	if _, err := c.Fast(ctx); err != nil {
		t.Fatal(err)
	}
	pr, _, _ := c.Store.PR(ctx, a.Ref)
	if pr.Workspace.Comment != "triage asks: keep the old column?" {
		t.Fatalf("comment %q", pr.Workspace.Comment)
	}
}

func TestATrackedPullRequestIsWatchedAndKeepsItsReason(t *testing.T) {
	s := snapshot()
	s.Author, s.PullRequest.ReviewRequests, s.ReviewRequests = "zoe", nil, nil
	gone := api.Ref{Owner: "acme", Repo: "api", Number: 404}
	gh := &fakeGH{remaining: 4000, byRef: map[api.Ref]api.Snapshot{s.Ref: s}}
	c := newCollector(t, gh)
	ctx := context.Background()
	for _, ref := range []api.Ref{s.Ref, gone} {
		if err := setTracked(ctx, c.Store, ref); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if _, err := c.Fast(ctx); err != nil {
			t.Fatal(err)
		}
	}
	reads := map[api.Ref]int{}
	for _, r := range gh.read {
		reads[r]++
	}
	if reads[s.Ref] != 1 || reads[gone] != 1 || len(gh.stubRefs) != 1 || !slices.Equal(gh.stubRefs[0], []api.Ref{s.Ref}) {
		t.Fatalf("reads %v, stubs %v: want the tracked one read once then stub-checked, the missing one read once until the slow sweep", reads, gh.stubRefs)
	}
	if _, err := c.Slow(ctx); err != nil {
		t.Fatal(err)
	}
	p, ok, _ := c.Store.PR(ctx, s.Ref)
	if !ok || !slices.Equal(p.Reasons, []string{pr.ReasonTracked}) {
		t.Fatalf("after the slow sweep's recompute: %+v %v", p.Reasons, ok)
	}
}

func TestTrackReadsAtOnce(t *testing.T) {
	s := snapshot()
	s.Author = "zoe"
	typed := api.Ref{Owner: "Acme", Repo: "API", Number: 7}
	gh := &fakeGH{remaining: 4000, byRef: map[api.Ref]api.Snapshot{s.Ref: s, typed: s}}
	c := newCollector(t, gh)
	ctx := context.Background()
	got, err := c.ReadOne(ctx, typed)
	if err != nil || got.Snapshot.Ref.Unit() != s.Ref.Unit() || got.Snapshot.State != api.StateOpen {
		t.Fatalf("track %s: %+v, %v; want the name GitHub gives and its state", typed, got.Snapshot.Ref, err)
	}
	if _, ok, _ := c.Store.PR(ctx, s.Ref); ok {
		t.Fatal("stored before the track was admitted")
	}
	if err := c.Save(ctx, got); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := c.Store.PR(ctx, s.Ref); !ok {
		t.Fatal("not stored under its canonical name")
	}
	if _, ok, _ := c.Store.PR(ctx, typed); ok {
		t.Fatal("stored under the name as typed")
	}
	if _, err := c.ReadOne(ctx, api.Ref{Owner: "acme", Repo: "api", Number: 404}); !errors.Is(err, src.ErrNotFound) {
		t.Fatalf("a pull request GitHub did not return: %v", err)
	}
	gh.byRefErr = &api.BudgetError{Limit: api.Limit{Resource: api.ResourceGraphQL, Limit: 5000, Remaining: 50, ResetAt: now.Add(time.Hour)}}
	var low *api.BudgetError
	if _, err := c.ReadOne(ctx, s.Ref); !errors.As(err, &low) || low.Limit.ResetAt.IsZero() {
		t.Fatalf("a refused read keeps the budget error: %v", err)
	}
}

func TestTrackSortsGitHubFailures(t *testing.T) {
	saml := api.Ref{Owner: "acme", Repo: "api", Number: 9}
	gh := &fakeGH{remaining: 4321, refErrs: map[api.Ref]error{saml: errors.New("acme/api#9: Resource protected by organization SAML enforcement")}}
	c := newCollector(t, gh)
	ctx := context.Background()
	_, err := c.ReadOne(ctx, saml)
	if !errors.Is(err, src.ErrUnavailable) || errors.Is(err, src.ErrNotFound) || !strings.Contains(err.Error(), "SAML") {
		t.Fatalf("a per pull request error: %v", err)
	}
	gh.byRefErr = errors.New("HTTP 502")
	if _, err := c.ReadOne(ctx, saml); !errors.Is(err, src.ErrUnavailable) {
		t.Fatalf("a failed call: %v", err)
	}
}

func requestEvents(t *testing.T, c *Collector, ref api.Ref) []pr.Event {
	t.Helper()
	evs, err := eventsOf(context.Background(), c, ref)
	if err != nil {
		t.Fatal(err)
	}
	return slices.DeleteFunc(evs, func(e pr.Event) bool { return e.Kind != pr.KindReviewRequest })
}

func TestReRequestIsANewEvent(t *testing.T) {
	s := snapshot()
	gh := &fakeGH{remaining: 4000}
	c := newCollector(t, gh)
	refresh(t, c, gh, s, now)
	again := s.ReviewRequests[0].At.Add(3 * time.Hour)
	s.RemovedReviews = []api.ReviewRequestEvent{{Reviewer: "me", Actor: "ana", At: again.Add(-time.Hour)}}
	s.ReviewRequests = append(s.ReviewRequests, api.ReviewRequestEvent{Reviewer: "me", Actor: "ana", At: again})
	s.UpdatedAt = s.UpdatedAt.Add(time.Hour)
	refresh(t, c, gh, s, now.Add(time.Hour))
	reqs := requestEvents(t, c, s.Ref)
	if len(reqs) != 2 || reqs[0].Data != "me" || reqs[1].Data != "me" || !reqs[1].At.Equal(again) {
		t.Fatalf("a re-request must be stored as its own event: %+v", reqs)
	}
}

func TestAMergedEventStoredWithoutItsMergerIsFilledIn(t *testing.T) {
	ctx := context.Background()
	s := snapshot()
	other := s
	other.Ref.Number = 8
	gh := &fakeGH{remaining: 4000}
	c := newCollector(t, gh)
	for _, snap := range []api.Snapshot{s, other} {
		refresh(t, c, gh, snap, now)
	}
	s.State, s.MergedAt, s.MergedBy, s.MergeCommit = "MERGED", s.UpdatedAt.Add(time.Hour), "bia", "m9"
	s.UpdatedAt = s.MergedAt
	other.State, other.MergedAt, other.MergedBy, other.MergeCommit, other.UpdatedAt = s.State, s.MergedAt, s.MergedBy, s.MergeCommit, s.UpdatedAt
	stored := []pr.Event{
		{PR: s.Ref, Kind: pr.KindMerged, At: s.MergedAt},
		{PR: other.Ref, Kind: pr.KindMerged, Actor: "carla", At: s.MergedAt},
	}
	if err := c.Store.RecordEvents(ctx, stored); err != nil {
		t.Fatal(err)
	}
	for _, snap := range []api.Snapshot{s, other} {
		refresh(t, c, gh, snap, now.Add(time.Hour))
	}
	merged := func(ref api.Ref) []pr.Event {
		evs, err := eventsOf(ctx, c, ref)
		if err != nil {
			t.Fatal(err)
		}
		return slices.DeleteFunc(evs, func(e pr.Event) bool { return e.Kind != pr.KindMerged })
	}
	if m := merged(s.Ref); len(m) != 1 || m[0].Actor != "bia" || m[0].Data != "m9" {
		t.Fatalf("empty fields must be filled in: %+v", m)
	}
	if m := merged(other.Ref); len(m) != 1 || m[0].Actor != "carla" || m[0].Data != "m9" {
		t.Fatalf("only empty fields change: %+v", m)
	}
}

func TestATruncatedTimelineIsLogged(t *testing.T) {
	s := snapshot()
	s.TimelineMissed = 3
	gh := &fakeGH{remaining: 4000}
	c := newCollector(t, gh)
	var logged []string
	c.Log = func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }
	refresh(t, c, gh, s, now)
	if !slices.Contains(logged, "acme/api#7: the timeline read left out its 3 earliest events") {
		t.Fatalf("logged %q", logged)
	}
}

func TestSlowNotesViewerChange(t *testing.T) {
	gh := &fakeGH{remaining: 4000}
	c := newCollector(t, gh)
	topics := heard(c)
	ctx := context.Background()
	noted := func() bool {
		from := len(*topics)
		if _, err := c.Slow(ctx); err != nil {
			t.Fatal(err)
		}
		return slices.Contains((*topics)[from:], sdk.TopicViewer)
	}
	if !noted() {
		t.Fatal("the first login was not noted")
	}
	if noted() {
		t.Fatal("an unchanged login was noted")
	}
	gh.viewer = "renamed"
	if !noted() {
		t.Fatal("a changed login was not noted")
	}
	if got, _ := c.Store.Viewer(ctx); got != "renamed" {
		t.Fatalf("stored %q", got)
	}
}

func TestSlowNotesTeamMembershipChange(t *testing.T) {
	gh := &fakeGH{remaining: 4000}
	c := newCollector(t, gh)
	topics := heard(c)
	ctx := context.Background()
	viewerNoted := func() bool {
		from := len(*topics)
		if _, err := c.Slow(ctx); err != nil {
			t.Fatal(err)
		}
		return slices.Contains((*topics)[from:], sdk.TopicViewer)
	}
	viewerNoted()
	if viewerNoted() {
		t.Fatal("unchanged teams noted")
	}
	gh.members = []string{"dylan", "me"}
	if !viewerNoted() {
		t.Fatal("a membership change was not noted")
	}
	if viewerNoted() {
		t.Fatal("repeated teams noted")
	}
}

func TestATransientStubErrorIsAskedAgainNextSweep(t *testing.T) {
	ctx := context.Background()
	s := snapshot()
	gh := &fakeGH{remaining: 4000, byRef: map[api.Ref]api.Snapshot{s.Ref: s}}
	c := newCollector(t, gh)
	if err := c.Store.UpsertPR(ctx, pr.PR{Ref: s.Ref, State: "OPEN", Reasons: []string{"author"}}); err != nil {
		t.Fatal(err)
	}
	gh.refErrs = map[api.Ref]error{s.Ref: errors.New("acme/api#7: Something went wrong while executing your query")}
	if _, err := c.Fast(ctx); err != nil {
		t.Fatal(err)
	}
	gh.refErrs = nil
	if _, err := c.Fast(ctx); err != nil {
		t.Fatal(err)
	}
	if len(gh.stubRefs) != 2 || !slices.Contains(gh.stubRefs[1], s.Ref) {
		t.Fatalf("stub reads %v: a transient error silenced the pull request", gh.stubRefs)
	}
}
