package change

import (
	"slices"
	"strings"
	"time"

	dgh "github.com/wloophq/wloop/internal/domain/github"
	"github.com/wloophq/wloop/internal/domain/pr"
)

type AddressInput struct {
	PR     pr.Facts
	Viewer string
	// Handled is the submittedAt of the newest review a previous triage
	// round covered; zero when there was none.
	Handled time.Time
	// SkipReviewers are logins whose reviews never start a triage round.
	SkipReviewers []string
}

// ReviewsToAddress returns the reviewers whose reviews or open threads a
// triage round on my pull request should cover, and the fingerprint to record
// once it starts. Without a previous round, reviews count only on the current
// head (older ones predate a push, which is how I usually answer by hand),
// while an open thread whose last word is someone else's always counts.
func ReviewsToAddress(in AddressInput) (reviewers []string, fingerprint string) {
	f := in.PR
	if f.State != dgh.StateOpen {
		return nil, ""
	}
	var newest time.Time
	add := func(who string, at time.Time) {
		if !slices.ContainsFunc(reviewers, func(s string) bool { return strings.EqualFold(s, who) }) {
			reviewers = append(reviewers, who)
		}
		if at.After(newest) {
			newest = at
		}
	}
	for _, t := range f.OpenThreads {
		switch {
		case t.LastAuthor == "", strings.EqualFold(t.LastAuthor, in.Viewer):
		case containsFold(in.SkipReviewers, t.LastAuthor):
		case !t.LastAt.After(in.Handled):
		default:
			add(t.LastAuthor, t.LastAt)
		}
	}
	for _, r := range f.Reviews {
		switch {
		case r.SubmittedAt.IsZero(), !r.HasContent:
		case strings.EqualFold(r.Author, in.Viewer), strings.EqualFold(r.Author, f.Author):
		case containsFold(in.SkipReviewers, r.Author):
		case r.State != pr.ReviewChangesRequested && r.State != pr.ReviewCommented && r.State != pr.ReviewApproved:
		case !r.SubmittedAt.After(in.Handled):
		case in.Handled.IsZero() && r.CommitOid != f.HeadRefOid:
		default:
			add(r.Author, r.SubmittedAt)
		}
	}
	if len(reviewers) == 0 {
		return nil, ""
	}
	return reviewers, newest.UTC().Format(time.RFC3339)
}

// FailingChecks returns the checks that failed on the head of my pull
// request once every check has finished, unless a triage round already
// covered that head. Ignored checks neither fire a round nor hold it back.
func FailingChecks(f pr.Facts, handledHead string, ignore []string) []string {
	if f.State != dgh.StateOpen || f.HeadRefOid == "" || f.HeadRefOid == handledHead {
		return nil
	}
	var failed []string
	for _, c := range f.Checks {
		switch {
		case containsFold(ignore, c.Name):
		case c.Pending:
			return nil
		case c.Failed && !slices.Contains(failed, c.Name):
			failed = append(failed, c.Name)
		}
	}
	return failed
}
