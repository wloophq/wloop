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
	// Silent says a draft effect applies without the draft's post, so a
	// person may close the conversation without the message.
	Silent bool
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
	{Name: EffectPlanCleared, Applies: AppliesTurn, Doc: "Drops the unit's pending plan items."},
	{Name: EffectGuidanceHanded, Applies: AppliesTurn, Doc: "Hands the guidance queued on the unit to the turn, and queues it back when the turn is not sent."},
	{Name: EffectRoundSent, Applies: AppliesTurn, Doc: "Records the round-sent event on the unit when a person sends the turn."},
	{Name: EffectTaskNudged, Applies: AppliesDraft, Doc: "Writes the last_nudge property of the draft's task and of every open task holding one of the items it lists. It says the people were nudged, so it needs the draft's post."},
	{Name: EffectUnitClosed, Applies: AppliesDraft, Doc: "Records the closure of the draft's unit through its plugin, which unwatches it after its warm period, then ends the open tasks that hold the unit. It applies without the draft's post too.", Plugin: true, Silent: true},
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

// SilentEffect says whether draft effect name applies without the draft's post.
func SilentEffect(name string) bool {
	e, ok := EffectOf(name)
	return ok && e.Silent
}

// EffectOf is the effect named name.
func EffectOf(name string) (Effect, bool) {
	i := slices.IndexFunc(Effects, func(e Effect) bool { return e.Name == name })
	if i < 0 {
		return Effect{}, false
	}
	return Effects[i], true
}
