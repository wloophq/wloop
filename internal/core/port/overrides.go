package port

import "slices"

// The host's overrides of a unit, by the name a plugin's SetOverride sets
// and its RoundState reads. A time is RFC 3339 in UTC, a flag "true", and ""
// is unset.
const (
	OverrideMutedUntil           = "muted_until"
	OverrideMuteUntilStateChange = "mute_until_state_change"
	OverrideMutedKind            = "muted_kind"
	OverrideMutedWaitingOn       = "muted_waiting_on"
	OverrideNextGuidance         = "next_guidance"
	// OverrideHold is the user's pause of the unit's rounds, which a
	// plugin reads as RoundState.Paused and never sets.
	OverrideHold = "hold"
)

// OverrideTrue is a set flag's value.
const OverrideTrue = "true"

// Overrides are every host override name.
var Overrides = []string{OverrideMutedUntil, OverrideMuteUntilStateChange, OverrideMutedKind, OverrideMutedWaitingOn,
	OverrideNextGuidance, OverrideHold}

// PluginOverrides are the host overrides a plugin may set.
var PluginOverrides = slices.DeleteFunc(slices.Clone(Overrides), func(n string) bool { return n == OverrideHold })
