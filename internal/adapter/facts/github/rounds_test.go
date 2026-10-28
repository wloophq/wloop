package factsgithub_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
)

// reviewedWithoutWorkspace is a pull request with new commits since my
// review, a recent review turn and no review workspace.
func (w *world) reviewedWithoutWorkspace(n int, author string, edit func(*dinv.PR, *pr.Facts)) pr.Ref {
	r := w.add(n, author, func(row *dinv.PR, f *pr.Facts) {
		row.HeadOid, row.HeadBranch = "c2", "feature"
		f.Commits = []pr.Commit{{Oid: "c1", Parents: 1}, {Oid: "c2", Parents: 1}}
		f.Reviews = []pr.Review{{ID: "r1", State: pr.ReviewCommented, Author: me, SubmittedAt: t0.Add(-2 * time.Hour), CommitOid: "c1"}}
		if edit != nil {
			edit(row, f)
		}
	})
	w.books[r.Unit()] = turn.Book{Unit: r.Unit(), Turns: []turn.Turn{{Seq: 1, Intent: turn.Intent(dintent.Review), Role: "pr.review", State: turn.Done, SentAt: t0.Add(-3 * time.Hour)}}}
	return r
}

func TestAFollowupWithoutItsWorkspaceNamesWhyItIsNotFollowed(t *testing.T) {
	for name, c := range map[string]struct {
		author string
		setup  func(w *world, r pr.Ref)
		reason string
	}{
		"I opened it": {author: me, reason: "you opened it"},
		"I develop it in a worktree": {author: "alice", setup: func(w *world, _ pr.Ref) {
			w.workspace(wsdomain.Workspace{ID: "dev", Path: "/w/dev", DisplayName: "feature-work", Branch: "feature"})
		}, reason: "you develop it in feature-work"},
		"its review ended and it merged": {author: "alice", setup: func(w *world, r pr.Ref) {
			row := w.rows[r]
			row.State = "MERGED"
			w.rows[r] = row
			w.ended[r.Unit()] = true
		}, reason: "its review ended"},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld()
			r := w.reviewedWithoutWorkspace(7, c.author, nil)
			if c.setup != nil {
				c.setup(w, r)
			}
			f := w.facts(t, w.provider(), r.Unit())
			if f["pr.review.action"] != "" || f["pr.review.reason"] != c.reason {
				t.Fatalf("review = %q, %q; want no action, %q", f["pr.review.action"], f["pr.review.reason"], c.reason)
			}
		})
	}
}

func TestAVerdictTheLoopRecordedOnTheHeadCountsAsMyReviewOfIt(t *testing.T) {
	w := newWorld()
	r := w.review(7, nil)
	if f := w.facts(t, w.provider(), r.Unit()); f["pr.review.action"] != "follow-up" {
		t.Fatalf("without a recorded verdict: %v", f["pr.review.action"])
	}
	approve := record.Record{State: record.Done, Summary: "approved", Outcome: &record.Outcome{Verdict: "APPROVE"}}
	w.books[r.Unit()] = turn.Book{Unit: r.Unit(),
		Turns:   []turn.Turn{{Seq: 1, Intent: turn.Intent(dintent.Review), Role: "pr.review", Trigger: turn.ByHand, State: turn.Done, Head: "c2", SentAt: t0.Add(-90 * time.Minute)}},
		Records: []turn.Record{{TurnSeq: 1, Head: "c2", At: t0.Add(-time.Hour), Record: approve}}}
	if f := w.facts(t, w.provider(), r.Unit()); f["pr.review.action"] != "" {
		t.Fatalf("with a verdict recorded on the head: %v (%v)", f["pr.review.action"], f["pr.review.reason"])
	}
}

func TestAFirstRoundWaitsWhenIDevelopThePRInAWorktree(t *testing.T) {
	w := newWorld()
	r := w.requested(3, nil)
	w.workspace(wsdomain.Workspace{ID: "dev", Path: "/w/dev", DisplayName: "feature-work", Branch: "feature"})
	if f := w.facts(t, w.provider(), r.Unit()); f["pr.first.due"] != false || f["pr.first.reason"] != "you develop it in feature-work" {
		t.Fatalf("first = %v, %q", f["pr.first.due"], f["pr.first.reason"])
	}
}

func TestAnUnlinkedWorkspaceOnMyPRsBranchIsItsAuthorWorkspace(t *testing.T) {
	for name, c := range map[string]struct {
		ws   wsdomain.Workspace
		want bool
	}{
		"a worktree on the branch":            {wsdomain.Workspace{ID: "dev", Path: "/w/dev", DisplayName: "dev", Branch: "feature"}, true},
		"the main checkout on the branch":     {wsdomain.Workspace{ID: "main2", Path: "/w/app2", DisplayName: "app2", Branch: "feature", IsMain: true}, true},
		"a worktree on another host":          {wsdomain.Workspace{ID: "far", Path: "/w/far", DisplayName: "far", Branch: "feature", HostID: "ssh:box"}, false},
		"a worktree on another branch":        {wsdomain.Workspace{ID: "other", Path: "/w/other", DisplayName: "other", Branch: "other"}, false},
		"the main checkout on default branch": {wsdomain.Workspace{ID: "main3", Path: "/w/app3", DisplayName: "app3", Branch: "main", IsMain: true}, false},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld()
			r := w.add(5, me, nil)
			w.workspace(c.ws)
			carded, err := w.provider().Carded(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got := slices.Contains(carded, r.Unit()); got != c.want {
				t.Fatalf("carded %v, want the PR carded: %v", carded, c.want)
			}
		})
	}
}

func TestCardedListsThePullRequestsInUnitOrder(t *testing.T) {
	w := newWorld()
	var want []unit.Unit
	for _, n := range []int{9, 4, 6} {
		r := w.add(n, me, func(row *dinv.PR, _ *pr.Facts) { row.HeadBranch = "b" + string(rune('0'+n)) })
		w.workspace(wsdomain.Workspace{ID: r.String(), Path: "/w/" + r.String(), DisplayName: "dev", Branch: "b" + string(rune('0'+n)), LinkedPR: n})
		want = append(want, r.Unit())
	}
	slices.SortFunc(want, func(a, b unit.Unit) int {
		if a.Less(b) {
			return -1
		}
		return 1
	})
	got, err := w.provider().Carded(context.Background())
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("Carded = %v, %v; want %v", got, err, want)
	}
}

func TestAConflictCountsFromWhenTheInventorySawIt(t *testing.T) {
	w := newWorld()
	r := w.mine(5, func(row *dinv.PR, _ *pr.Facts) {
		row.Mergeable, row.MergeableAt = "CONFLICTING", t0.Add(-2*time.Hour)
	})
	af, err := w.provider().Author(context.Background(), r.Unit(), false)
	if err != nil {
		t.Fatal(err)
	}
	if af.ConflictingFor != 2*time.Hour || af.Mergeable != "CONFLICTING" {
		t.Fatalf("conflicting for %v, mergeable %q", af.ConflictingFor, af.Mergeable)
	}
}

func TestSettleKeepsTheAgentsQuestionOnAnOpenPRNoLongerRequestingMe(t *testing.T) {
	w := newWorld()
	open := w.add(3, "alice", nil)
	closed := w.add(4, "alice", func(row *dinv.PR, _ *pr.Facts) { row.State = "CLOSED" })
	b := inventory.StartBlock{Question: "trust", Root: "/w/app", Folder: "/w/app", Round: "first"}
	for _, r := range []pr.Ref{open, closed} {
		w.blocks[r] = dinv.Event{PR: r, Kind: dinv.KindRoundBlocked, Key: b.Key(t0.Add(-time.Hour)), At: t0.Add(-time.Hour), Data: "could not start"}
	}
	w.rounds.Saved()
	if err := w.provider().Settle(context.Background(), state{w.rounds}); err != nil {
		t.Fatal(err)
	}
	if len(w.recorded) != 1 || w.recorded[0].PR != closed || w.recorded[0].Kind != dinv.KindRoundUnblocked || w.recorded[0].Data != "it is closed" {
		t.Fatalf("recorded %+v", w.recorded)
	}
}

func TestAStoreOfReviewTurnsAndAProfileMyReviewProposesNoNewRound(t *testing.T) {
	w := newWorld()
	r := w.review(7, nil)
	approve := record.Record{State: record.Done, Summary: "approved", Outcome: &record.Outcome{Verdict: "APPROVE"}}
	w.books[r.Unit()] = turn.Book{Unit: r.Unit(),
		Turns: []turn.Turn{
			{Seq: 1, Intent: "review", Role: "pr.review", Trigger: turn.ByHand, State: turn.Done, Head: "c1", SentAt: t0.Add(-3 * time.Hour)},
			{Seq: 2, Intent: "my-review", Role: "pr.review", Trigger: turn.ByHand, State: turn.Done, Head: "c2", SentAt: t0.Add(-90 * time.Minute)},
		},
		Records: []turn.Record{{TurnSeq: 2, Head: "c2", At: t0.Add(-time.Hour), Record: approve}}}
	if f := w.facts(t, w.provider(), r.Unit()); f["pr.review.action"] != "" {
		t.Fatalf("my-review's verdict on the head: %v (%v)", f["pr.review.action"], f["pr.review.reason"])
	}
	b := w.books[r.Unit()]
	b.Turns[1].Role = ""
	w.books[r.Unit()] = b
	if f := w.facts(t, w.provider(), r.Unit()); f["pr.review.action"] != "follow-up" {
		t.Fatalf("a verdict of an intent holding no review role counted: %v", f["pr.review.action"])
	}
}
