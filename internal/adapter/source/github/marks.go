package ghsource

// The GitHub plugin's round marks, as the host's in-process GitHub paths
// still write and read them: the removal's skip and an address round's
// reason.
const (
	MarkReviewed          = "reviewed"
	MarkAddressedReviews  = "addressed.reviews"
	MarkAddressedChecks   = "addressed.checks"
	MarkAddressedConflict = "addressed.conflict"
	MarkFirstSkipped      = "first.skipped"
)
