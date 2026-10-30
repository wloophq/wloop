package collect

import (
	"testing"
	"time"

	"github.com/wloophq/wloop/plugins/github/api"
	"github.com/wloophq/wloop/plugins/github/pr"
)

func snapshot() api.Snapshot {
	t := func(d string) time.Time { v, _ := time.Parse(time.RFC3339, d); return v }
	s := api.Snapshot{URL: "https://github.com/acme/api/pull/7", CreatedAt: t("2026-10-01T10:00:00Z"), UpdatedAt: t("2026-10-05T09:00:00Z")}
	s.Ref = api.Ref{Owner: "acme", Repo: "api", Number: 7}
	s.Title, s.Author, s.State, s.HeadRefOid, s.ChecksState = "Batch export", "ana", "OPEN", "h2", "SUCCESS"
	s.CommitsDated = []api.DatedCommit{{Oid: "h1", Parents: 1, At: t("2026-10-01T10:00:00Z")}, {Oid: "m1", Parents: 2, At: t("2026-10-01T11:00:00Z")}, {Oid: "h2", Parents: 1, At: t("2026-10-02T10:00:00Z")}}
	s.Reviews = []pr.Review{{ID: "r1", State: "COMMENTED", Author: "me", SubmittedAt: t("2026-10-03T10:00:00Z"), CommitOid: "h2"}}
	s.Comments = []pr.Comment{{ID: "c1", Author: "ana", CreatedAt: t("2026-10-04T10:00:00Z")}}
	s.ReviewRequests = []api.ReviewRequestEvent{{Reviewer: "me", At: t("2026-10-02T11:00:00Z")}}
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
		case pr.KindReview:
			if e.Data != "COMMENTED:h2" {
				t.Fatalf("review data: %q", e.Data)
			}
		case pr.KindPush:
			if e.Key == "m1" {
				t.Fatal("merge commit must not be a push")
			}
		case pr.KindCheck:
			if e.Key != "h2:SUCCESS" || e.Data != "SUCCESS" || !e.At.Equal(now) {
				t.Fatalf("check: %+v", e)
			}
		case pr.KindReviewRequest:
			if e.Key != "me:2026-10-02T11:00:00Z" || e.Data != "me" || e.Actor != "ana" {
				t.Fatalf("request: %+v", e)
			}
		}
	}
	want := map[string]int{pr.KindOpened: 1, pr.KindPush: 2, pr.KindReview: 1, pr.KindComment: 1, pr.KindReviewRequest: 1, pr.KindCheck: 1}
	for k, n := range want {
		if kinds[k] != n {
			t.Fatalf("%s: got %d want %d (%v)", k, kinds[k], n, kinds)
		}
	}
}

func TestEventsFromEmitsCheckOnlyOnChange(t *testing.T) {
	now := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	for _, e := range EventsFrom(snapshot(), "SUCCESS", now) {
		if e.Kind == pr.KindCheck {
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
		if e.Kind == pr.KindMerged && e.At.Equal(s.MergedAt) {
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
	key := func(evs []pr.Event) pr.Event {
		for _, e := range evs {
			if e.Kind == pr.KindReview {
				return e
			}
		}
		t.Fatal("no review event")
		return pr.Event{}
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
		if e.Kind == pr.KindMerged {
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
	s.Drafts = []api.DraftEvent{{Actor: "ana", At: at(1)}, {Actor: "ana", At: at(3)}}
	s.RemovedReviews = []api.ReviewRequestEvent{{Reviewer: "team:acme/search", Actor: "ana", At: at(2)}}
	var drafts, removed []pr.Event
	for _, e := range EventsFrom(s, "", s.UpdatedAt) {
		switch e.Kind {
		case pr.KindDraft:
			drafts = append(drafts, e)
		case pr.KindReviewRequestRemoved:
			removed = append(removed, e)
		}
	}
	if len(drafts) != 2 || drafts[0].Actor != "ana" || !drafts[1].At.Equal(at(3)) {
		t.Fatalf("drafts: %+v", drafts)
	}
	if len(removed) != 1 || removed[0].Key != "team:acme/search:2026-10-03T02:00:00Z" || removed[0].Data != "team:acme/search" || removed[0].Actor != "ana" {
		t.Fatalf("removed: %+v", removed)
	}
}
