// Package change holds the rules that decide what a change to a pull request
// calls for: a follow-up, a triage round, a board move.
package change

import (
	"slices"
	"strings"
	"time"

	dgh "github.com/wloophq/wloop/internal/domain/github"
	"github.com/wloophq/wloop/internal/domain/pr"
)

type ReviewerAction int

const (
	ReviewerNothing ReviewerAction = iota
	ReviewerComplete
	ReviewerFollowup
)

type ReviewerInput struct {
	PR              pr.Facts
	Viewer          string
	WorkspaceStatus string
	// CompletedStatus is the board's completed column; empty when it has none.
	CompletedStatus string
	// TrustedOwners are the repository owners automated rounds may run for.
	TrustedOwners []string
	// TrustedAuthors are logins that pass the author association check.
	TrustedAuthors  []string
	LastFingerprint string
	// Recorded is the loop's record of my last verdict; zero when none.
	Recorded RecordedReview
}

// RecordedReview is the head a turn's record covered when it published a
// verdict, and when it was recorded. GitHub moves a review's commit to later
// pushes, so this head is the baseline for the review it recorded.
type RecordedReview struct {
	Head string
	At   time.Time
}

type ReviewerDecision struct {
	Action ReviewerAction
	Reason string
	// Fingerprint identifies what a follow-up covers: the submittedAt of my
	// last review and the head, so a round that published nothing still lets
	// the next push start another, marked when a re-request started it. On
	// ReviewerNothing it is set only to upgrade a fingerprint stored without
	// the head.
	Fingerprint  string
	LastReviewAt time.Time
}

const reRequested = "my review was re-requested"

// reRequestMark sets apart the fingerprint of a round a re-request started,
// so a re-request after a settled round on the same head fires once more.
const reRequestMark = "#rr"

var trustedAssociations = []string{"MEMBER", "COLLABORATOR", "OWNER"}

func Reviewer(in ReviewerInput) ReviewerDecision {
	f := in.PR
	switch f.State {
	case dgh.StateMerged, dgh.StateClosed:
		if in.CompletedStatus != "" && in.WorkspaceStatus == in.CompletedStatus {
			return ReviewerDecision{Reason: "already completed"}
		}
		return ReviewerDecision{Action: ReviewerComplete, Reason: strings.ToLower(f.State)}
	}
	if f.IsDraft {
		return ReviewerDecision{Reason: "draft"}
	}
	if why := Untrusted(f, in.TrustedOwners, in.TrustedAuthors); why != "" {
		return ReviewerDecision{Reason: why}
	}

	last, ok := LastReview(f.Reviews, in.Viewer)
	if !ok {
		return ReviewerDecision{Reason: "no submitted review by me yet"}
	}
	if in.Recorded.Head != "" && !last.SubmittedAt.After(in.Recorded.At) {
		last.CommitOid = in.Recorded.Head
	}
	trigger := reviewerTrigger(f, in.Viewer, last)
	if trigger == "" {
		return ReviewerDecision{Reason: "nothing new since my last review"}
	}
	reviewed := last.SubmittedAt.UTC().Format(time.RFC3339)
	headFP := reviewed + "@" + f.HeadRefOid
	fp := headFP
	if trigger == reRequested {
		fp += reRequestMark
	}
	switch in.LastFingerprint {
	case fp, headFP + reRequestMark:
		return ReviewerDecision{Reason: trigger + ", but this head already got its follow-up"}
	case reviewed:
		return ReviewerDecision{Reason: trigger + ", but this review already got its follow-up", Fingerprint: fp}
	}
	return ReviewerDecision{Action: ReviewerFollowup, Reason: trigger, Fingerprint: fp, LastReviewAt: last.SubmittedAt}
}

// Untrusted says why an automated round may not run on f, or "".
func Untrusted(f pr.Facts, owners, authors []string) string {
	switch {
	case f.IsCrossRepository:
		return "from a fork, not trusted for automated rounds"
	case !slices.Contains(trustedAssociations, f.AuthorAssociation) && (f.Author == "" || !containsFold(authors, f.Author)):
		return "author association " + f.AuthorAssociation + " is not trusted for automated rounds"
	case !containsFold(owners, f.Owner):
		return "owner " + f.Owner + " is not in the owners allowlist"
	}
	return ""
}

// LastReview is my newest submitted review. PENDING has no submittedAt.
// DISMISSED counts: repositories that dismiss stale reviews on push turn my
// last real review into DISMISSED exactly when the PR changed.
func LastReview(reviews []pr.Review, viewer string) (pr.Review, bool) {
	var last pr.Review
	found := false
	for _, r := range reviews {
		if !strings.EqualFold(r.Author, viewer) || r.SubmittedAt.IsZero() {
			continue
		}
		switch r.State {
		case pr.ReviewApproved, pr.ReviewChangesRequested, pr.ReviewCommented, pr.ReviewDismissed:
		default:
			continue
		}
		if !found || r.SubmittedAt.After(last.SubmittedAt) {
			last, found = r, true
		}
	}
	return last, found
}

func reviewerTrigger(f pr.Facts, viewer string, last pr.Review) string {
	if containsFold(f.ReviewRequests, viewer) {
		return reRequested
	}
	if HeadMoved(f.Commits, f.HeadRefOid, last.CommitOid) {
		return "new commits since my review"
	}
	// After an approval only code changes or an explicit re-request matter:
	// an author "thanks" must not reopen a full round.
	if last.State == pr.ReviewApproved {
		return ""
	}
	for _, r := range f.Reviews {
		if strings.EqualFold(r.Author, f.Author) && r.SubmittedAt.After(last.SubmittedAt) {
			return "the author replied in a review"
		}
	}
	for _, c := range f.Comments {
		if strings.EqualFold(c.Author, f.Author) && c.CreatedAt.After(last.SubmittedAt) {
			return "the author commented"
		}
	}
	return ""
}

// HeadMoved reports whether the head has commits after the reviewed one
// beyond merges from the base branch. When the reviewed commit is no longer in
// the listed history (force push, or more than the listed commits since),
// every listed commit counts.
func HeadMoved(commits []pr.Commit, headOid, reviewedOid string) bool {
	if headOid == "" || headOid == reviewedOid {
		return false
	}
	after := commits
	for i, c := range commits {
		if c.Oid == reviewedOid {
			after = commits[i+1:]
			break
		}
	}
	if len(after) == 0 {
		return true
	}
	return slices.ContainsFunc(after, func(c pr.Commit) bool { return c.Parents == 1 })
}

func containsFold(list []string, s string) bool {
	return slices.ContainsFunc(list, func(v string) bool { return strings.EqualFold(v, s) })
}
