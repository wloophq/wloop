package pr

import (
	"strings"
	"time"
)

// RequestedTeams are the teams of requests, review requests as
// "team:<org>/<slug>", as <org>-<slug>.
func RequestedTeams(requests []string) []string {
	var out []string
	for _, r := range requests {
		if t, ok := strings.CutPrefix(r, "team:"); ok {
			out = append(out, strings.Replace(t, "/", "-", 1))
		}
	}
	return out
}

// LastReviewVerdict is the state of the viewer's newest submitted review,
// any state; "" when none.
func LastReviewVerdict(reviews []Review) string {
	var state string
	var at time.Time
	for _, r := range reviews {
		if r.ByViewer && !r.SubmittedAt.IsZero() && r.SubmittedAt.After(at) {
			state, at = r.State, r.SubmittedAt
		}
	}
	return state
}

// Prior is the viewer's latest submitted review: the head it covered, when
// it was, and the latest verdict that was not a comment. GitHub can return a
// review without its commit; such a review gives no head.
type Prior struct {
	Head    string
	At      time.Time
	Verdict string
}

// PriorReview is the viewer's Prior of reviews; false when the viewer
// submitted no review.
func PriorReview(reviews []Review) (Prior, bool) {
	var p Prior
	reviewed := false
	var headAt, verdictAt time.Time
	for _, r := range reviews {
		if !r.ByViewer || r.SubmittedAt.IsZero() {
			continue
		}
		switch r.State {
		case ReviewApproved, ReviewChangesRequested, ReviewCommented, ReviewDismissed:
		default:
			continue
		}
		reviewed = true
		if r.CommitOid != "" && r.SubmittedAt.After(headAt) {
			p.Head, headAt = r.CommitOid, r.SubmittedAt
		}
		if r.SubmittedAt.After(p.At) {
			p.At = r.SubmittedAt
		}
		if r.State != ReviewCommented && r.SubmittedAt.After(verdictAt) {
			p.Verdict, verdictAt = r.State, r.SubmittedAt
		}
	}
	return p, reviewed
}
