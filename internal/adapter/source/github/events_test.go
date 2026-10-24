package ghsource

import (
	"testing"
	"time"

	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/github"
)

func snapshot() github.Snapshot {
	t := func(d string) time.Time { v, _ := time.Parse(time.RFC3339, d); return v }
	s := github.Snapshot{URL: "https://github.com/acme/api/pull/7", CreatedAt: t("2026-10-01T10:00:00Z"), UpdatedAt: t("2026-10-05T09:00:00Z")}
	s.Ref = github.Ref{Owner: "acme", Repo: "api", Number: 7}
	s.Title, s.Author, s.State, s.HeadRefOid, s.ChecksState = "Batch export", "ana", "OPEN", "h2", "SUCCESS"
	s.CommitsDated = []github.DatedCommit{{Oid: "h1", Parents: 1, At: t("2026-10-01T10:00:00Z")}, {Oid: "m1", Parents: 2, At: t("2026-10-01T11:00:00Z")}, {Oid: "h2", Parents: 1, At: t("2026-10-02T10:00:00Z")}}
	s.Reviews = []pr.Review{{ID: "r1", State: "COMMENTED", Author: "me", SubmittedAt: t("2026-10-03T10:00:00Z"), CommitOid: "h2"}}
	s.Comments = []pr.Comment{{ID: "c1", Author: "ana", CreatedAt: t("2026-10-04T10:00:00Z")}}
	s.ReviewRequests = []github.ReviewRequestEvent{{Reviewer: "me", At: t("2026-10-02T11:00:00Z")}}
	s.PullRequest.ReviewRequests = []string{"me"}
	s.LastEditedAt, s.ChangedFiles = t("2026-10-05T08:00:00Z"), 4
	return s
}

func TestEventsFrom(t *testing.T) {
	now := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	evs := EventsFrom(snapshot(), "", now)
	kinds := map[string]int{}
	for _, e := range evs {
		kinds[e.Kind]++
		switch e.Kind {
		case dinv.KindReview:
			if e.Data != "COMMENTED:h2" {
				t.Fatalf("review data: %q", e.Data)
			}
		case dinv.KindPush:
			if e.Key == "m1" {
				t.Fatal("merge commit must not be a push")
			}
		case dinv.KindCheck:
			if e.Key != "h2:SUCCESS" || e.Data != "SUCCESS" || !e.At.Equal(now) {
				t.Fatalf("check: %+v", e)
			}
		case dinv.KindReviewRequest:
			if e.Key != "me:2026-10-02T11:00:00Z" || e.Data != "me" || e.Actor != "ana" || e.Reviewer() != "me" {
				t.Fatalf("request: %+v", e)
			}
		}
	}
	want := map[string]int{dinv.KindOpened: 1, dinv.KindPush: 2, dinv.KindReview: 1, dinv.KindComment: 1, dinv.KindReviewRequest: 1, dinv.KindCheck: 1}
	for k, n := range want {
		if kinds[k] != n {
			t.Fatalf("%s: got %d want %d (%v)", k, kinds[k], n, kinds)
		}
	}
}

func TestEventsFromEmitsCheckOnlyOnChange(t *testing.T) {
	now := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	for _, e := range EventsFrom(snapshot(), "SUCCESS", now) {
		if e.Kind == dinv.KindCheck {
			t.Fatal("unchanged checks produced an event")
		}
	}
}

func TestEventsFromMerged(t *testing.T) {
	s := snapshot()
	s.State = "MERGED"
	s.MergedAt = s.UpdatedAt
	found := false
	for _, e := range EventsFrom(s, "", s.UpdatedAt) {
		if e.Kind == dinv.KindMerged && e.At.Equal(s.MergedAt) {
			found = true
		}
	}
	if !found {
		t.Fatal("no merged event")
	}
}

func TestEventsFromDismissedReviewIsADistinctEvent(t *testing.T) {
	s := snapshot()
	s.Reviews[0].State = pr.ReviewChangesRequested
	before := EventsFrom(s, "", s.UpdatedAt)
	s.Reviews[0].State = pr.ReviewDismissed
	after := EventsFrom(s, "", s.UpdatedAt)
	key := func(evs []dinv.Event) dinv.Event {
		for _, e := range evs {
			if e.Kind == dinv.KindReview {
				return e
			}
		}
		t.Fatal("no review event")
		return dinv.Event{}
	}
	b, a := key(before), key(after)
	if b.Key == a.Key || a.Data != "DISMISSED:h2" || !a.At.Equal(b.At) {
		t.Fatalf("dismissal must be a new event at the review time: before %+v after %+v", b, a)
	}
}

func TestMergedCarriesTheMergerAndCommit(t *testing.T) {
	s := snapshot()
	s.State, s.MergedAt, s.MergedBy, s.MergeCommit = "MERGED", s.UpdatedAt, "bia", "m9"
	for _, e := range EventsFrom(s, "", s.UpdatedAt) {
		if e.Kind == dinv.KindMerged {
			if e.Actor != "bia" || e.Data != "m9" || !e.At.Equal(s.MergedAt) {
				t.Fatalf("merged: %+v", e)
			}
			return
		}
	}
	t.Fatal("no merged event")
}

func TestDraftAndRemovedRequestEvents(t *testing.T) {
	s := snapshot()
	at := func(h int) time.Time { return time.Date(2026, 10, 3, h, 0, 0, 0, time.UTC) }
	s.Drafts = []github.DraftEvent{{Actor: "ana", At: at(1)}, {Actor: "ana", At: at(3)}}
	s.RemovedReviews = []github.ReviewRequestEvent{{Reviewer: "team:acme/search", Actor: "ana", At: at(2)}}
	var drafts, removed []dinv.Event
	for _, e := range EventsFrom(s, "", s.UpdatedAt) {
		switch e.Kind {
		case dinv.KindDraft:
			drafts = append(drafts, e)
		case dinv.KindReviewRequestRemoved:
			removed = append(removed, e)
		}
	}
	if len(drafts) != 2 || drafts[0].Actor != "ana" || !drafts[1].At.Equal(at(3)) {
		t.Fatalf("drafts: %+v", drafts)
	}
	if len(removed) != 1 || removed[0].Key != "team:acme/search:2026-10-03T02:00:00Z" || removed[0].Reviewer() != "team:acme/search" || removed[0].Actor != "ana" {
		t.Fatalf("removed: %+v", removed)
	}
}
