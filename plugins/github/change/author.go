package change

import "github.com/wloophq/wloop/plugins/github/pr"

// Approved also covers repositories without required reviews, where
// reviewDecision is null.
func Approved(f pr.Facts) bool {
	switch f.ReviewDecision {
	case pr.ReviewApproved:
		return true
	case "":
		approved := false
		for _, r := range f.LatestReviews {
			switch r.State {
			case pr.ReviewChangesRequested:
				return false
			case pr.ReviewApproved:
				approved = true
			}
		}
		return approved
	}
	return false
}

// ChecksPass treats a repository without checks as passing.
func ChecksPass(f pr.Facts) bool {
	return f.ChecksState == "" || f.ChecksState == "SUCCESS"
}
