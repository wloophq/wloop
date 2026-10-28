package change

import (
	"testing"

	"github.com/wloophq/wloop/plugins/github/pr"
)

func TestApproved(t *testing.T) {
	for _, c := range []struct {
		name string
		f    pr.Facts
		want bool
	}{
		{"approved", pr.Facts{ReviewDecision: pr.ReviewApproved}, true},
		{"review required", pr.Facts{ReviewDecision: "REVIEW_REQUIRED"}, false},
		{"no required reviews, approved by latest", pr.Facts{LatestReviews: []pr.Review{{State: pr.ReviewApproved}}}, true},
		{"no required reviews, a change request blocks", pr.Facts{LatestReviews: []pr.Review{{State: pr.ReviewApproved}, {State: pr.ReviewChangesRequested}}}, false},
		{"no required reviews, nobody approved", pr.Facts{}, false},
	} {
		if got := Approved(c.f); got != c.want {
			t.Errorf("%s: Approved = %v", c.name, got)
		}
	}
}

func TestChecksPass(t *testing.T) {
	for state, want := range map[string]bool{"SUCCESS": true, "": true, "FAILURE": false, "PENDING": false} {
		if got := ChecksPass(pr.Facts{ChecksState: state}); got != want {
			t.Errorf("ChecksPass(%q) = %v", state, got)
		}
	}
}
