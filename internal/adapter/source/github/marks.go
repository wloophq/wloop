package ghsource

import (
	"github.com/wloophq/wloop/internal/domain/github/change"
)

// The GitHub plugin's round marks, as the host's in-process GitHub paths
// still write and read them: the removal's skip, a held review run by hand
// and an address round's reason.
const (
	MarkReviewed          = "reviewed"
	MarkAddressedReviews  = "addressed.reviews"
	MarkAddressedChecks   = "addressed.checks"
	MarkAddressedConflict = "addressed.conflict"
	MarkFirstSkipped      = "first.skipped"
)

// ReviewedMarks cover the reviews a review round that went out saw; nil
// when they leave nothing to cover.
func ReviewedMarks(in change.ReviewerInput) map[string]string {
	if fp := change.Reviewer(in).Fingerprint; fp != "" {
		return map[string]string{MarkReviewed: fp}
	}
	return nil
}
