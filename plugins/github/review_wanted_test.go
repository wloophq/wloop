package github

import (
	"context"
	"slices"
	"testing"
	"time"

	sdk "github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/plan"
	"github.com/wloophq/wloop/plugins/github/pr"
	"github.com/wloophq/wloop/plugins/github/store"
)

func factIs(fs []src.Fact, name string) bool {
	return slices.ContainsFunc(fs, func(f src.Fact) bool { return f.Name == name && f.Value.Bool })
}

// requestedByName is the bundled review_wanted, pr.review_requested_me.
func requestedByName(fs []src.Fact) bool { return factIs(fs, "pr.review_requested_me") }

// othersInAcme is a review_wanted that wants every pull request of acme
// that is not mine.
func othersInAcme(fs []src.Fact) bool { return ownerIs(fs, "acme") && !factIs(fs, "pr.mine") }

// unrequested stores a pull request of bruno that asks no one's review,
// read in full, edited by edit.
func unrequested(t *testing.T, p *Plugin, edit func(*pr.PR, *store.Detail)) pr.Ref {
	t.Helper()
	ctx := context.Background()
	if err := p.store().SetViewer(ctx, "ana"); err != nil {
		t.Fatal(err)
	}
	row := pr.PR{Ref: ref7, Title: "Change 7", Author: "bruno", State: "OPEN", HeadOid: "h7", HeadBranch: "feat/7",
		BaseBranch: "main", CreatedAt: testAt.Add(-48 * time.Hour), Reasons: []string{"repo"}}
	d := store.Detail{Version: store.DetailVersion, ReadAt: testAt.Add(-time.Minute), AuthorAssociation: "MEMBER"}
	if edit != nil {
		edit(&row, &d)
	}
	if err := p.store().UpsertPR(ctx, row); err != nil {
		t.Fatal(err)
	}
	if err := p.store().SaveDetail(ctx, row.Ref, d); err != nil {
		t.Fatal(err)
	}
	return row.Ref
}

// firstRound is the first round of plan, if it plans one.
func firstRound(got src.Plan) (src.Round, bool) {
	i := slices.IndexFunc(got.Rounds, func(r src.Round) bool { return r.Name == plan.RoundFirst })
	if i < 0 {
		return src.Round{}, false
	}
	return got.Rounds[i], true
}

// firstDue says whether Plan and the report each find ref's first round
// due, failing when they disagree.
func firstDue(t *testing.T, p *Plugin, h *fakeHost, ref pr.Ref) (bool, src.Round) {
	t.Helper()
	got, err := p.Plan(context.Background(), ref.Unit(), src.RoundState{})
	if err != nil {
		t.Fatal(err)
	}
	round, planned := firstRound(got)
	if err := p.Notify(context.Background(), []sdk.Unit{ref.Unit()}, []sdk.Topic{sdk.TopicRounds}); err != nil {
		t.Fatal(err)
	}
	reported := factIs(h.lastReport(t, ref).Facts, "pr.first.due")
	due := planned && round.NotDue == ""
	if due != reported {
		t.Fatalf("Plan plans %+v (%v) but the report says pr.first.due = %v", round, planned, reported)
	}
	return due, round
}

func TestByDefaultOnlyARequestByNameStartsAFirstRound(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		requests []string
		due      bool
	}{
		"requested by name":   {[]string{"ana"}, true},
		"requested of a team": {[]string{"team:acme/core"}, false},
		"requested of nobody": {nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			h := &fakeHost{listing: apiListing, wanted: requestedByName}
			p := started(t, &githubGraphQL{}, h)
			ref := unrequested(t, p, func(row *pr.PR, _ *store.Detail) { row.ReviewRequests = c.requests })
			if due, round := firstDue(t, p, h, ref); due != c.due {
				t.Fatalf("first round due = %v (%+v), want %v", due, round, c.due)
			}
		})
	}
}

func TestAPullRequestTheProfileWantsStartsItsFirstRoundOnce(t *testing.T) {
	t.Parallel()
	h := &fakeHost{listing: apiListing, wanted: othersInAcme}
	p := started(t, &githubGraphQL{}, h)
	ref := unrequested(t, p, nil)
	due, round := firstDue(t, p, h, ref)
	if !due || round.Reason != "the profile wants my review" {
		t.Fatalf("first round %+v due %v, want one the profile's wish starts", round, due)
	}
	if s := h.lastReport(t, ref).Situation; s.Kind != "review" || s.WaitingOn != "me" {
		t.Fatalf("situation %+v, want my review waited on", s)
	}
	h.mu.Lock()
	h.books = []src.Book{{Unit: ref.Unit(), Turns: []src.Turn{{Seq: 1, Intent: "review", Role: "pr.review", State: "done",
		At: testAt.Add(-time.Hour), SentAt: testAt.Add(-time.Hour), Head: "h7"}}}}
	h.mu.Unlock()
	if due, round := firstDue(t, p, h, ref); due {
		t.Fatalf("a first round restarts after its review turn: %+v", round)
	}
	unrequested(t, p, func(row *pr.PR, _ *store.Detail) { row.HeadOid = "h8" })
	got, err := p.Plan(context.Background(), ref.Unit(), src.RoundState{})
	if err != nil {
		t.Fatal(err)
	}
	if round, ok := firstRound(got); ok {
		t.Fatalf("a new head with no review workspace plans a first round again: %+v", round)
	}
}

func TestAWantedPullRequestIReviewedStartsNoFirstRound(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		edit  func(*pr.PR, *store.Detail)
		event *src.Event
	}{
		"my review on GitHub": {edit: func(_ *pr.PR, d *store.Detail) {
			d.Reviews = []pr.Review{{ID: "r1", State: pr.ReviewCommented, Author: "ana", SubmittedAt: testAt.Add(-2 * time.Hour), CommitOid: "h6"}}
		}},
		"my review's event": {event: &src.Event{Unit: ref7.Unit(), Kind: pr.KindReview, Key: "r1", Actor: "ana", At: testAt.Add(-2 * time.Hour),
			Data: []byte("COMMENTED:h6")}},
	} {
		t.Run(name, func(t *testing.T) {
			h := &fakeHost{listing: apiListing, wanted: othersInAcme}
			p := started(t, &githubGraphQL{}, h)
			ref := unrequested(t, p, c.edit)
			if c.event != nil {
				h.record(*c.event)
			}
			got, err := p.Plan(context.Background(), ref.Unit(), src.RoundState{})
			if err != nil {
				t.Fatal(err)
			}
			if round, ok := firstRound(got); ok {
				t.Fatalf("a pull request I reviewed plans a first round: %+v", round)
			}
		})
	}
}

func TestAWantedPullRequestStillAnswersToTheTrustRules(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		edit func(*pr.PR, *store.Detail)
		why  string
	}{
		"a fork": {func(row *pr.PR, _ *store.Detail) { row.IsFork = true }, "from a fork, not trusted for automated rounds"},
		"an author with no association": {func(_ *pr.PR, d *store.Detail) { d.AuthorAssociation = "NONE" },
			"author association NONE is not trusted for automated rounds"},
		"an owner outside the allowlist": {func(row *pr.PR, _ *store.Detail) { row.Ref = pr.Ref{Owner: "zeta", Repo: "api", Number: 7} },
			"owner zeta is not in the owners allowlist"},
	} {
		t.Run(name, func(t *testing.T) {
			h := &fakeHost{listing: apiListing, wanted: func(fs []src.Fact) bool { return !factIs(fs, "pr.mine") }}
			p := started(t, &githubGraphQL{}, h)
			ref := unrequested(t, p, c.edit)
			due, round := firstDue(t, p, h, ref)
			if due || round.NotDue != c.why {
				t.Fatalf("first round %+v due %v, want it refused: %s", round, due, c.why)
			}
		})
	}
}

func TestABlockedPullRequestStaysBlockedWhileTheProfileWantsMyReview(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		wanted    func([]src.Fact) bool
		unblocked string
	}{
		"wanted":     {othersInAcme, ""},
		"not wanted": {requestedByName, "my review is no longer wanted"},
	} {
		t.Run(name, func(t *testing.T) {
			h := &fakeHost{listing: apiListing, wanted: c.wanted}
			p := started(t, &githubGraphQL{}, h)
			ref := unrequested(t, p, func(row *pr.PR, _ *store.Detail) { row.IsDraft = true })
			h.record(src.Event{Unit: ref.Unit(), Kind: pr.KindRoundBlocked, At: testAt.Add(-time.Hour), Data: []byte("feat/7 is checked out in /src/api")})
			if err := p.Notify(context.Background(), []sdk.Unit{ref.Unit()}, []sdk.Topic{sdk.TopicRounds}); err != nil {
				t.Fatal(err)
			}
			var got string
			h.mu.Lock()
			for _, e := range h.events {
				if e.Kind == pr.KindRoundUnblocked {
					got = string(e.Data)
				}
			}
			h.mu.Unlock()
			if got != c.unblocked {
				t.Fatalf("unblocked %q, want %q", got, c.unblocked)
			}
		})
	}
}

func TestAReviewedPullRequestRequestedByNameLeavesItsFollowUpToTheFirstRound(t *testing.T) {
	t.Parallel()
	h := &fakeHost{listing: apiListing, wanted: requestedByName}
	p := started(t, &githubGraphQL{}, h)
	ref := unrequested(t, p, func(row *pr.PR, _ *store.Detail) { row.ReviewRequests = []string{"ana"} })
	h.mu.Lock()
	h.books = []src.Book{{Unit: ref.Unit(), Turns: []src.Turn{{Seq: 1, Intent: "review", Role: "pr.review", State: "done",
		At: testAt.Add(-time.Hour), SentAt: testAt.Add(-time.Hour), Head: "h7"}}}}
	h.mu.Unlock()
	if err := p.Notify(context.Background(), []sdk.Unit{ref.Unit()}, []sdk.Topic{sdk.TopicRounds}); err != nil {
		t.Fatal(err)
	}
	var reason string
	for _, f := range h.lastReport(t, ref).Facts {
		if f.Name == "pr.review.reason" {
			reason = f.Value.String
		}
	}
	if reason != "my review was requested, which starts a first round" {
		t.Fatalf("pr.review.reason = %q", reason)
	}
}
