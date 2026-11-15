package ghsource

// The GitHub plugin's round marks, as the host's in-process GitHub paths
// still write and read them: the first-round skip of a removal or a
// dismissal, and an address round's reason.
const (
	MarkAddressedReviews  = "addressed.reviews"
	MarkAddressedChecks   = "addressed.checks"
	MarkAddressedConflict = "addressed.conflict"
	MarkFirstSkipped      = "first.skipped"
)
