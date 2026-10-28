package collect

import (
	"cmp"
	"time"

	"github.com/wloophq/wloop/plugins/github/api"
	"github.com/wloophq/wloop/plugins/github/pr"
	"github.com/wloophq/wloop/plugins/github/store"
)

// EventsFrom turns a snapshot into GitHub events. prevChecks is the stored
// checks state, so a check event is produced only when it changes.
func EventsFrom(s api.Snapshot, prevChecks string, now time.Time) []pr.Event {
	ref := s.Ref
	var evs []pr.Event
	add := func(kind, key, actor string, at time.Time, data string) {
		if at.IsZero() {
			return
		}
		evs = append(evs, pr.Event{PR: ref, Kind: kind, Key: key, Actor: actor, At: at, Data: data})
	}
	add(pr.KindOpened, "", s.Author, s.CreatedAt, "")
	for _, c := range s.CommitsDated {
		if c.Parents <= 1 {
			add(pr.KindPush, c.Oid, s.Author, c.At, "")
		}
	}
	for _, r := range s.Reviews {
		add(pr.KindReview, reviewKey(r), r.Author, r.SubmittedAt, r.State+":"+r.CommitOid)
	}
	for _, c := range s.Comments {
		add(pr.KindComment, c.ID, c.Author, c.CreatedAt, "")
	}
	for _, rr := range s.ReviewRequests {
		add(pr.KindReviewRequest, pr.RequestKey(rr.Reviewer, rr.At), cmp.Or(rr.Actor, s.Author), rr.At, rr.Reviewer)
	}
	for _, rr := range s.RemovedReviews {
		add(pr.KindReviewRequestRemoved, pr.RequestKey(rr.Reviewer, rr.At), rr.Actor, rr.At, rr.Reviewer)
	}
	for _, d := range s.Drafts {
		add(pr.KindDraft, "", d.Actor, d.At, "")
	}
	if s.ChecksState != "" && s.HeadRefOid != "" && s.ChecksState != prevChecks {
		add(pr.KindCheck, s.HeadRefOid+":"+s.ChecksState, "", now, s.ChecksState)
	}
	add(pr.KindReady, "", s.Author, s.ReadyAt, "")
	switch s.State {
	case "MERGED":
		add(pr.KindMerged, "", s.MergedBy, s.MergedAt, s.MergeCommit)
	case "CLOSED":
		add(pr.KindClosed, "", "", s.ClosedAt, "")
	}
	return evs
}

// reviewKey gives a dismissal its own key: events are insert-only, so the
// review's earlier state stays stored and the dismissal must follow it.
func reviewKey(r pr.Review) string {
	if r.State == pr.ReviewDismissed {
		return r.ID + ":" + r.State
	}
	return r.ID
}

// clampPushes moves a new push whose commit date, which the author controls,
// precedes the newest stored push to its first sighting, so the head is never
// older than the commit it replaced.
func clampPushes(evs []pr.Event, f store.StoredFacts, now time.Time) {
	var newest time.Time
	for _, at := range f.Pushes {
		if at.After(newest) {
			newest = at
		}
	}
	for i, e := range evs {
		if _, known := f.Pushes[e.Key]; e.Kind == pr.KindPush && !known && e.At.Before(newest) {
			evs[i].At = now
		}
	}
}
