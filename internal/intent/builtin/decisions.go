package builtin

import (
	"slices"

	"github.com/wloophq/wloop/internal/domain/decision"
)

// SurfaceDecisions is decisions.toml, of the default and the plugin layers.
const SurfaceDecisions Surface = "decisions"

// DecisionOpen is the primary of a unit's decision that opens the unit at its source.
const DecisionOpen = "open"

var decisionDocs = map[decision.Kind]string{
	decision.Answer:      "An agent's question or a post waits for the user's answer.",
	decision.ApprovePlan: "An agent's plan waits for the user's approval.",
	decision.SendDraft:   "A draft waits for the user to send, edit or drop it.",
	decision.ReviewStep:  "A step of a review waits for the user's look.",
	decision.LastStep:    "A unit waits only for its last step at its source, such as a merge or a closure, which the user takes there.",
	decision.PickSomeone: "A unit waits for the user to pick who takes it next, such as a reviewer.",
	decision.Act:         "A proposal or a trust request waits for the user to act on it.",
}

// DecisionKinds are the decision kinds as enum values, in decision.Kinds order: what decisions.toml maps gate
// kinds, situations and signals to.
func DecisionKinds() []Value {
	out := make([]Value, 0, len(decision.Kinds))
	for _, k := range decision.Kinds {
		out = append(out, Value{Name: string(k), Doc: decisionDocs[k]})
	}
	return out
}

func decisionKindNames() []string {
	out := make([]string, len(decision.Kinds))
	for i, k := range decision.Kinds {
		out[i] = string(k)
	}
	return out
}

// pluralText is a text a person reads at path of surface s, written as a string or as a table of its CLDR
// plural forms.
func pluralText(s Surface, path, doc string) []Key {
	out := []Key{{Path: path, In: s, Kind: KindAny, Localized: true, Doc: doc + " A string, or a table of its CLDR plural forms, `other` required."}}
	for _, f := range PluralForms {
		out = append(out, Key{Path: path + "." + f, In: s, Kind: KindText, Localized: true, Doc: "Its " + f + " form."})
	}
	return out
}

var decisionsGrammar = slices.Concat([]Key{
	{Path: "gates", In: SurfaceDecisions, Kind: KindTable, Doc: "The decision kind of an open gate, by gate kind; only the default layer's file maps gates."},
	{Path: "gates.*", In: SurfaceDecisions, Kind: KindEnum, Values: DecisionKinds, Doc: "One gate kind's decision kind."},
	{Path: "situations", In: SurfaceDecisions, Kind: KindTable, Doc: "The decision kind of a unit in a situation its plugin derives, by situation; only a plugin's layer maps them."},
	{Path: "situations.*", In: SurfaceDecisions, Kind: KindEnum, Values: DecisionKinds, Doc: "One situation's decision kind."},
	{Path: "signals", In: SurfaceDecisions, Kind: KindTable, Doc: "The decision kind of a unit whose link signal is on, by signal; only a plugin's layer maps them."},
	{Path: "signals.*", In: SurfaceDecisions, Kind: KindEnum, Values: DecisionKinds, Doc: "One signal's decision kind."},
	{Path: "primary", In: SurfaceDecisions, Kind: KindTable, Doc: "What a queue offers first on a unit of the plugin, by decision kind; only a plugin's layer sets it."},
	{Path: "primary.*", In: SurfaceDecisions, Kind: KindText, Doc: "A catalog action of the plugin's units, or `open`, which opens the unit at its source."},
	{Path: "kinds", In: SurfaceDecisions, Kind: KindTable, Doc: "Each decision kind's view and words, by kind; only the default layer's file sets them."},
	{Path: "kinds.*", In: SurfaceDecisions, Kind: KindTable, Doc: "One decision kind."},
	{Path: "kinds.*.view", In: SurfaceDecisions, Kind: KindText, Required: true, Doc: "The view of views.toml that lists the kind's tasks, which its queue walks. Required."},
	{Path: "kinds.*.primary", In: SurfaceDecisions, Kind: KindText, Doc: "The gate intent a queue offers first on a gate of the kind; empty offers the gate's first."},
	{Path: "kinds.*.queue", In: SurfaceDecisions, Kind: KindText, Localized: true, Required: true, Doc: "The title of the kind's queue. Required."},
	{Path: "kinds.*.empty", In: SurfaceDecisions, Kind: KindText, Localized: true, Required: true, Doc: "What the kind's queue says when it holds nothing. Required."},
}, pluralText(SurfaceDecisions, "kinds.*.noun", "What one of the kind's tasks is, which the briefing counts, such as \"question\"."),
	pluralText(SurfaceDecisions, "kinds.*.clause", "The briefing's clause on the kind, `{counter}` where its count goes, such as \"the agent has {counter} for you\"."))
