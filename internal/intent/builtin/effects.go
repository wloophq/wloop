package builtin

import (
	"slices"

	"github.com/wloophq/wloop/internal/domain/unit"
)

// Applies says what an effect applies on: a sent turn (an intent's effects)
// or a sent draft (a draft.propose's effects and an intent's [drafts]
// allowed_effects).
type Applies int

const (
	AppliesTurn Applies = 1 << iota
	AppliesDraft
)

// Effect is what sending a turn or a draft applies, by name.
type Effect struct {
	Name string
	// Provider is the provider whose turns or drafts alone may name it; ""
	// any.
	Provider unit.ProviderID
	Applies  Applies
	Doc      string
	// Plugin says a draft effect applies through the unit's plugin, so it
	// waits while that plugin is not built.
	Plugin bool
}

// The effects the host applies by name.
const (
	EffectPlanCleared    = "plan.cleared"
	EffectGuidanceHanded = "turn.guidance_handed"
	EffectRoundSent      = "turn.round_sent"
	EffectTaskNudged     = "task.nudged"
	EffectUnitClosed     = "unit.closed"
)

// Effects is the effect vocabulary of turns and drafts: the core's, then
// each plugin's.
var Effects = slices.Clone(coreEffects)

var coreEffects = []Effect{
	{EffectPlanCleared, "", AppliesTurn, "Drops the unit's pending plan items.", false},
	{EffectGuidanceHanded, "", AppliesTurn, "Hands the guidance queued on the unit to the turn, and queues it back when the turn is not sent.", false},
	{EffectRoundSent, "", AppliesTurn, "Records the round-sent event on the unit when a person sends the turn.", false},
	{EffectTaskNudged, "", AppliesDraft, "Stamps the last nudge of the draft's task, or of every live waiting task of its unit when it names none.", false},
	{EffectUnitClosed, "", AppliesDraft, "Records the closure of the draft's unit, which ends its close task and unwatches it after its warm period; through the unit's plugin.", true},
}

// EffectNames are the names of the draft effects: what a draft's effects, a
// draft.propose's effects and an intent's allowed_effects list.
func EffectNames() []string { return effectNames(AppliesDraft) }

// TurnEffectNames are the names of the turn effects: what an intent's
// effects list.
func TurnEffectNames() []string { return effectNames(AppliesTurn) }

func effectNames(a Applies) []string {
	var out []string
	for _, e := range Effects {
		if e.Applies&a != 0 {
			out = append(out, e.Name)
		}
	}
	return out
}

// EffectOf is the effect named name.
func EffectOf(name string) (Effect, bool) {
	i := slices.IndexFunc(Effects, func(e Effect) bool { return e.Name == name })
	if i < 0 {
		return Effect{}, false
	}
	return Effects[i], true
}
