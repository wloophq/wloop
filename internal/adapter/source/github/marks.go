package ghsource

import (
	"time"

	"github.com/wloophq/wloop/internal/domain/github/change"
	"github.com/wloophq/wloop/internal/domain/round"
)

// TakenOverMarks forget the reviews the address rounds covered, so a pull
// request taken over starts its triage from none of them.
func TakenOverMarks() map[string]string { return map[string]string{round.AddressedReviews: ""} }

// AddressedMarks cover the reviews so far, at at, by a round asked for by
// hand.
func AddressedMarks(at time.Time) map[string]string {
	return map[string]string{round.AddressedReviews: at.UTC().Format(time.RFC3339)}
}

// ReviewedMarks cover the reviews a review round that went out saw; nil
// when they leave nothing to cover.
func ReviewedMarks(in change.ReviewerInput) map[string]string {
	if fp := change.Reviewer(in).Fingerprint; fp != "" {
		return map[string]string{round.Reviewed: fp}
	}
	return nil
}
