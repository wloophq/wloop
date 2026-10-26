package builtin

import "github.com/wloophq/wloop/internal/domain/unit"

// OnSentNames are the effects an intent's on_sent may list: what sending
// its turn applies.
var OnSentNames = []string{"plan.drop_pending", "event.round_sent", "event.nudged", "record_round", "keeps_guidance", "mark_reviewed", "mark_addressed"}

// Effect is an effect a sent draft applies: the name a draft's effects, a
// draft.propose's on_posted and an intent's draft_effects list.
type Effect struct {
	Name string
	// Provider is the provider whose drafts alone may declare it; "" any.
	Provider unit.ProviderID
	Doc      string
	// Plugin says it applies through the unit's plugin, so it waits while
	// that plugin is not built.
	Plugin bool
}

// The effects code applies by name: a plugin's Module.OnPosted keys and the
// publishers that apply one as they post.
const (
	EffectEntryNudged = "entry.nudged"
	EffectUnitClosed  = "unit.closed"
	EffectPRNudged    = "pr.nudged"
)

// Effects is the effect vocabulary of drafts.
var Effects = []Effect{
	{EffectEntryNudged, "", "Stamps the last nudge of the draft's entry, or of every live waiting entry of its unit when it names none.", false},
	{EffectUnitClosed, "", "Records the closure of the draft's unit, which ends its close entry and unwatches it after its warm period; through the unit's plugin.", true},
	{EffectPRNudged, unit.GitHub, "Records the nudged event on the pull request, which the nudge refusal reads; applied by the GitHub publisher as it posts.", false},
}

// EffectNames are the names of Effects.
func EffectNames() []string {
	out := make([]string, len(Effects))
	for i, e := range Effects {
		out[i] = e.Name
	}
	return out
}
