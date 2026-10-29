package port

import "slices"

// The host's overrides of a unit, by the name a plugin's SetOverride sets
// and its RoundState reads. A flag is "true", and "" is unset.
const (
	OverrideNextGuidance = "next_guidance"
	// OverrideHold is the user's pause of the unit's rounds, which a
	// plugin reads as RoundState.Paused and never sets.
	OverrideHold = "hold"
)

// OverrideTrue is a set flag's value.
const OverrideTrue = "true"

// Overrides are every host override name.
var Overrides = []string{OverrideNextGuidance, OverrideHold}

// PluginOverrides are the host overrides a plugin may set.
var PluginOverrides = slices.DeleteFunc(slices.Clone(Overrides), func(n string) bool { return n == OverrideHold })
