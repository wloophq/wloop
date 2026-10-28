package builtin

import "github.com/wloophq/wloop/internal/domain/gate"

// gatesGrammar is the keys of gates.toml.
var gatesGrammar = []Key{
	{Path: "gates", In: SurfaceGates, Kind: KindMap, Doc: "The gates a profile names, by name, in the order the file declares them, a user layer's new ones after the bundled."},
	{Path: "gates.*", In: SurfaceGates, Kind: KindTable, Doc: "A gate, by its name: the one a gate.open step, an intent's record.missing or a verb's gate opens; a gate the loop opens by kind takes the first of its kind. A user layer may set only some keys of an entry declared below it; a new entry sets the required ones."},
	{Path: "gates.*.kind", In: SurfaceGates, Kind: KindEnum, Values: gateKindValues, Required: true, Doc: "What the gate decides, which names the consequence of resolving it; required for a new gate."},
	{Path: "gates.*.label", In: SurfaceGates, Kind: KindText, Doc: "What the gate asks of you, as clients list it."},
	{Path: "gates.*.intents", In: SurfaceGates, Kind: KindMap, Doc: "What you may resolve the gate with, by intent, in the order clients offer them."},
	{Path: "gates.*.intents.*", In: SurfaceGates, Kind: KindTable, Doc: "An intent, by its name. A user layer may set only some keys of an entry declared below it; a new entry sets the required ones."},
	{Path: "gates.*.intents.*.label", In: SurfaceGates, Kind: KindText, Doc: "The name people read on its button."},
	{Path: "gates.*.intents.*.summary", In: SurfaceGates, Kind: KindText, Doc: "What resolving the gate with the intent does, as help and palettes show it."},
	{Path: "gates.*.intents.*.note", In: SurfaceGates, Kind: KindTemplate, Doc: "What the intent would do on a row that offers it, over the action facts."},
	{Path: "gates.*.intents.*.confirm", In: SurfaceGates, Kind: KindTemplate, Doc: "The dialog that confirms the intent on a row, over the action facts; empty asks nothing."},
	{Path: "gates.*.intents.*.status", In: SurfaceGates, Kind: KindEnum, Values: gateStatusValues, Required: true, Doc: "The status the intent resolves the gate to; required for a new intent."},
	{Path: "gates.*.intents.*.answer", In: SurfaceGates, Kind: KindBool, Default: "false", Doc: "The intent takes text, such as a question's answer."},
	{Path: "approval", In: SurfaceGates, Kind: KindTable, Doc: "How a gate is decided when no manifest or configuration says: the default of each kind. A user layer's mode replaces the mode and its window, its rules replace the rules."},
	{Path: "approval.mode", In: SurfaceGates, Kind: KindEnum, Values: GateModes, Doc: "The mode when no rule holds; the bundled file sets it. send and run release a gate at once, delay when its window ends unless you resolve it first, wait holds it for you, dry_run rejects a draft gate."},
	{Path: "approval.window", In: SurfaceGates, Kind: KindDur, Doc: "With mode delay only: how long you have to resolve the gate."},
	{Path: "approval.rule[]", In: SurfaceGates, Kind: KindTable, Doc: "A rule; the first whose when holds gives the mode."},
	{Path: "approval.rule[].when", In: SurfaceGates, Kind: KindPred, Doc: "When the rule applies, over the gate's facts: gate.kind and gate.name."},
	{Path: "approval.rule[].mode", In: SurfaceGates, Kind: KindEnum, Values: GateModes, Doc: "The rule's mode; dry_run with a when that pins gate.kind to any kind but draft fails to load."},
	{Path: "approval.rule[].window", In: SurfaceGates, Kind: KindDur, Doc: "With mode delay only: how long you have to resolve the gate."},
}

func gateKindValues() []Value {
	doc := map[gate.Kind]string{
		gate.KindDraft:    "A draft to send: resolving it publishes the draft.",
		gate.KindPlan:     "A run the plan holds: resolving it releases the plan item.",
		gate.KindQuestion: "An agent's question: resolving it delivers the answer to its session.",
		gate.KindPost:     "An agent's post: resolving it delivers the reply to its session.",
		gate.KindProposal: "Proposed task or item operations: resolving it applies them.",
		gate.KindTrust:    "A repository's first run: resolving it unblocks the run.",
		gate.KindStep:     "A workflow's gate.open: resolving it records the event gate_resolved on its task, which workflows react to.",
	}
	out := make([]Value, len(gate.Kinds))
	for i, k := range gate.Kinds {
		out[i] = Value{Name: string(k), Doc: doc[k]}
	}
	return out
}

func gateStatusValues() []Value {
	var out []Value
	for _, s := range []gate.Status{gate.Approved, gate.Rejected, gate.Answered, gate.Dismissed, gate.Superseded} {
		out = append(out, Value{Name: string(s)})
	}
	return out
}
