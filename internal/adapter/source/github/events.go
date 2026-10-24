package ghsource

import (
	"cmp"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/domain/event"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/github"
)

// EventsFrom turns a snapshot into GitHub events. prevChecks is the stored
// checks state, so a check event is produced only when it changes.
func EventsFrom(s github.Snapshot, prevChecks string, now time.Time) []dinv.Event {
	ref := s.Ref
	var evs []dinv.Event
	add := func(kind, key, actor string, at time.Time, data string) {
		if at.IsZero() {
			return
		}
		evs = append(evs, dinv.Event{PR: ref, Kind: kind, Key: key, Actor: actor, At: at, Data: data})
	}
	add(dinv.KindOpened, "", s.Author, s.CreatedAt, "")
	for _, c := range s.CommitsDated {
		if c.Parents <= 1 {
			add(dinv.KindPush, c.Oid, s.Author, c.At, "")
		}
	}
	for _, r := range s.Reviews {
		add(dinv.KindReview, reviewKey(r), r.Author, r.SubmittedAt, r.State+":"+r.CommitOid)
	}
	for _, c := range s.Comments {
		add(dinv.KindComment, c.ID, c.Author, c.CreatedAt, "")
	}
	for _, rr := range s.ReviewRequests {
		add(dinv.KindReviewRequest, event.RequestKey(rr.Reviewer, rr.At), cmp.Or(rr.Actor, s.Author), rr.At, rr.Reviewer)
	}
	for _, rr := range s.RemovedReviews {
		add(dinv.KindReviewRequestRemoved, event.RequestKey(rr.Reviewer, rr.At), rr.Actor, rr.At, rr.Reviewer)
	}
	for _, d := range s.Drafts {
		add(dinv.KindDraft, "", d.Actor, d.At, "")
	}
	if s.ChecksState != "" && s.HeadRefOid != "" && s.ChecksState != prevChecks {
		add(dinv.KindCheck, s.HeadRefOid+":"+s.ChecksState, "", now, s.ChecksState)
	}
	add(dinv.KindReady, "", s.Author, s.ReadyAt, "")
	switch s.State {
	case "MERGED":
		add(dinv.KindMerged, "", s.MergedBy, s.MergedAt, s.MergeCommit)
	case "CLOSED":
		add(dinv.KindClosed, "", "", s.ClosedAt, "")
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
func clampPushes(evs []dinv.Event, f inventory.StoredFacts, now time.Time) {
	var newest time.Time
	for _, at := range f.Pushes {
		if at.After(newest) {
			newest = at
		}
	}
	for i, e := range evs {
		if _, known := f.Pushes[e.Key]; e.Kind == dinv.KindPush && !known && e.At.Before(newest) {
			evs[i].At = now
		}
	}
}
