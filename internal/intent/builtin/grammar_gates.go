package builtin

import (
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
)

// gatesGrammar is the keys of gates.toml.
var gatesGrammar = []Key{
	{Path: "gates", In: SurfaceGates, Kind: KindMap, Doc: "The gates a profile names, by name, in the order the file declares them, a user layer's new ones after the bundled."},
	{Path: "gates.*", In: SurfaceGates, Kind: KindTable, Doc: "A gate, by its name: the one a gate.open step, an intent's record.missing or a verb's gate opens; a gate the loop opens by kind takes the first of its kind. A user layer may set only some keys of an entry declared below it; a new entry sets the required ones."},
	{Path: "gates.*.kind", In: SurfaceGates, Kind: KindEnum, Values: gateKindValues, Required: true, Doc: "What the gate decides, which names the consequence of resolving it; required for a new gate."},
	{Path: "gates.*.label", In: SurfaceGates, Localized: true, Kind: KindText, Doc: "What the gate asks of you, as clients list it."},
	{Path: "gates.*.text", In: SurfaceGates, Localized: true, Kind: KindTemplate, Doc: "The words of the gate's subject when the loop writes them, over the proposal facts: the first proposal gate words an item proposal of the ledger with it, and a verb's gate the proposal of its verb."},
	{Path: "gates.*.closes_with", In: SurfaceGates, Kind: KindText, Doc: "The intent, one that refuses the gate, the gate closes with when you end the task it is on. While the gate is open, nothing else ends the task: its items resolving leave it open, and a workflow's verb that would end it is refused, until you settle the gate. The task's unit ending (a pull request merged or closed) supersedes the gate as moot. Empty or left out, the gate neither holds its task nor closes with it."},
	{Path: "gates.*.intents", In: SurfaceGates, Kind: KindMap, Doc: "What you may resolve the gate with, by intent, in the order clients offer them."},
	{Path: "gates.*.intents.*", In: SurfaceGates, Kind: KindTable, Doc: "An intent, by its name. A user layer may set only some keys of an entry declared below it; a new entry sets the required ones."},
	{Path: "gates.*.intents.*.label", In: SurfaceGates, Localized: true, Kind: KindText, Doc: "The name people read on its button."},
	{Path: "gates.*.intents.*.summary", In: SurfaceGates, Localized: true, Kind: KindText, Doc: "What resolving the gate with the intent does, as help and palettes show it."},
	{Path: "gates.*.intents.*.note", In: SurfaceGates, Localized: true, Kind: KindTemplate, Doc: "What the intent would do on a row that offers it, over the action facts."},
	{Path: "gates.*.intents.*.confirm", In: SurfaceGates, Localized: true, Kind: KindTemplate, Doc: "The dialog that confirms the intent on a row, over the action facts; empty asks nothing."},
	{Path: "gates.*.intents.*.status", In: SurfaceGates, Kind: KindEnum, Values: gateStatusValues, Required: true, Doc: "The status the intent resolves the gate to; required for a new intent."},
	{Path: "gates.*.intents.*.answer", In: SurfaceGates, Kind: KindBool, Default: "false", Doc: "The intent takes text, such as a question's answer."},
	{Path: "gates.*.ends_task", In: SurfaceGates, Kind: KindMap, Doc: "What closing the gate does to a task the loop opened only to hold gates, by the status it closes with (superseded when its subject is withdrawn), once none of the task's gates is open; a status left out leaves the task open, and a user layer sets only the statuses it names."},
	{Path: "gates.*.ends_task.*", In: SurfaceGates, Kind: KindEnum, Values: endsTaskValues, Doc: "The category the task ends in."},
	{Path: "effects", In: SurfaceGates, Kind: KindMap, Doc: "What sending a draft does besides posting it, one sentence per effect, by effect name; a draft's card shows the sentences of the effects it carries. A user layer sets only the effects it names; an empty sentence takes a lower layer's off."},
	{Path: "effects.*", In: SurfaceGates, Localized: true, Kind: KindText, Doc: "The sentence, such as \"The task is marked as nudged.\"; the key is an effect a draft may carry."},
	{Path: "drafts", In: SurfaceGates, Kind: KindTable, Doc: "The words a draft's card and the Outbox name drafts with. A user layer sets only the words it names; an empty word takes a lower layer's off."},
	{Path: "drafts.by_hand", In: SurfaceGates, Localized: true, Kind: KindText, Doc: "The title of the Outbox's group of the drafts no workflow proposed and no purpose names: those written by hand."},
	{Path: "drafts.by_hand_about", In: SurfaceGates, Localized: true, Kind: KindText, Doc: "One line under that title."},
	{Path: "drafts.quoted", In: SurfaceGates, Localized: true, Kind: KindText, Doc: "What a draft's card says of a draft posted in the direct conversation of the person it is to, which quotes items of another conversation: `{conversation}` names that conversation."},
	{Path: "drafts.purposes", In: SurfaceGates, Kind: KindMap, Doc: "What a draft is for, by the purpose its proposer names (nudge, close, ...): its card's head, and the title of the Outbox's group of the drafts of that purpose no workflow proposed. A purpose with no word shows as it is named."},
	{Path: "drafts.purposes.*", In: SurfaceGates, Localized: true, Kind: KindText, Doc: "The purpose's word, such as \"Nudge\"."},
	{Path: "drafts.states", In: SurfaceGates, Kind: KindMap, Doc: "Where a draft's delivery stands, by state, as its card says it."},
	{Path: "drafts.states.*", In: SurfaceGates, Localized: true, Kind: KindText, Doc: "The state's words, such as \"failed to send\"; the key is a draft state: held, queued, sending, sent, failed or dropped."},
	{Path: "drafts.reasons", In: SurfaceGates, Kind: KindMap, Doc: "Why a dropped draft was withdrawn, by reason, as the Outbox says it."},
	{Path: "drafts.reasons.*", In: SurfaceGates, Localized: true, Kind: KindText, Doc: "The reason's words; the key is a withdraw reason: assigned, moot, outside, joined, ended, skipped or superseded."},
	{Path: "approval", In: SurfaceGates, Kind: KindTable, Doc: "How a gate is decided when no manifest or configuration says: the default of each kind. A user layer's mode replaces the mode and its window, its rules replace the rules."},
	{Path: "approval.mode", In: SurfaceGates, Kind: KindEnum, Values: GateModes, Doc: "The mode when no rule holds; the bundled file sets it. send and run release a gate at once, delay when its window ends unless you resolve it first, wait holds it for you, dry_run rejects a draft gate."},
	{Path: "approval.window", In: SurfaceGates, Kind: KindDur, Doc: "With mode delay only: how long you have to resolve the gate."},
	{Path: "approval.rule[]", In: SurfaceGates, Kind: KindTable, Doc: "A rule; the first whose when holds gives the mode."},
	{Path: "approval.rule[].when", In: SurfaceGates, Kind: KindPred, Doc: "When the rule applies, over the gate's facts: gate.kind and gate.name."},
	{Path: "approval.rule[].mode", In: SurfaceGates, Kind: KindEnum, Values: GateModes, Doc: "The rule's mode; dry_run with a when that pins gate.kind to any kind but draft fails to load."},
	{Path: "approval.rule[].window", In: SurfaceGates, Kind: KindDur, Doc: "With mode delay only: how long you have to resolve the gate."},
}

func endsTaskValues() []Value {
	return []Value{{Name: string(task.CategoryDone), Doc: "Done: the task leaves the lists for the Logbook."},
		{Name: string(task.CategoryDropped), Doc: "Dropped: the task goes to the trash."}}
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
