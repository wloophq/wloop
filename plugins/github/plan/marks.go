package plan

// The marks the rounds of a pull request write, by what they covered; the
// host keeps them and hands them back in the round state.
const (
	// MarkReviewed is my last review and the head on a pull request I review.
	MarkReviewed = "reviewed"
	// MarkAddressedReviews is the newest review an address round triaged on
	// a pull request I develop.
	MarkAddressedReviews = "addressed.reviews"
	// MarkAddressedChecks is the head whose failing checks an address round
	// covered.
	MarkAddressedChecks = "addressed.checks"
	// MarkAddressedConflict is the head whose conflict with the base an
	// address round covered.
	MarkAddressedConflict = "addressed.conflict"
	// MarkFirstSkipped is set when the user skipped the automatic first
	// round; the planner plans no other one.
	MarkFirstSkipped = "first.skipped"
)

// The rounds a plan names.
const (
	RoundFollowup = "follow-up"
	RoundFirst    = "first"
	RoundAddress  = "address"
)
