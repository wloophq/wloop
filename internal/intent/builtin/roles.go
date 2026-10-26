package builtin

// The roles Go resolves an intent by (Registry.ByRole).
const (
	RoleReview   = "round.review"
	RoleAddress  = "round.address"
	RoleNudge    = "nudge"
	RoleProbe    = "probe"
	RoleHeadless = "headless.default"
)

// Roles are what Go resolves an intent by, a manifest's role.
var Roles = []string{RoleReview, RoleAddress, RoleNudge, RoleProbe, RoleHeadless}

// BuildsBrief is what the brief intent builds.
const BuildsBrief = "brief"

// Builds are what a builder intent may build.
var Builds = []string{BuildsBrief}

// The values of a manifest's round_kind, cap and reason.
const (
	RoundKindReview    = "review"
	RoundKindAddress   = "address"
	CapRounds          = "rounds"
	ReasonAddressRound = "address_round"
)

// RoleRound is the round kind and cap an intent holding a role has when its
// manifest leaves them out.
type RoleRound struct{ RoundKind, Cap string }

// RoleRounds are the defaults by role; every intent with a round kind spends
// the round budget too, so no round or nudge goes out uncapped.
var RoleRounds = map[string]RoleRound{
	RoleReview:  {RoundKindReview, CapRounds},
	RoleAddress: {RoundKindAddress, CapRounds},
	RoleNudge:   {"", CapRounds},
}

var (
	RoundKinds  = []string{RoundKindReview, RoundKindAddress}
	Caps        = []string{CapRounds}
	TurnReasons = []string{ReasonAddressRound}
)
