package builtin

// SurfaceOmnibox is omnibox.toml: how the omnibox groups what it finds, and the verbs it knows.
const SurfaceOmnibox Surface = "omnibox"

var omniboxGrammar = []Key{
	{Path: "search", In: SurfaceOmnibox, Kind: KindTable, Doc: "How a search groups the tasks it finds."},
	{Path: "search.limit", In: SurfaceOmnibox, Kind: KindInt, Default: "5", Doc: "The most results a group shows; its count counts them all."},
	{Path: "search.other", In: SurfaceOmnibox, Kind: KindText, Localized: true, Doc: "The label of the last group, which holds what no group's view holds."},
	{Path: "search.groups[]", In: SurfaceOmnibox, Kind: KindTable, Doc: "A group, in order: a result goes to the first group whose view holds it. A user's list replaces the whole list."},
	{Path: "search.groups[].id", In: SurfaceOmnibox, Kind: KindText, Required: true, Doc: "The group's id. Required."},
	{Path: "search.groups[].label", In: SurfaceOmnibox, Kind: KindText, Localized: true, Required: true, Doc: "The group's label. Required."},
	{Path: "search.groups[].view", In: SurfaceOmnibox, Kind: KindText, Required: true, Doc: "The view of views.toml whose tasks the group holds. Required."},
	{Path: "verbs", In: SurfaceOmnibox, Kind: KindTable, Doc: "The verbs the omnibox knows, by id: typed first, a verb's word runs its target on what the rest names. No operation runs, from a verb or from the agent, that no verb targets."},
	{Path: "verbs.*", In: SurfaceOmnibox, Kind: KindTable, Doc: "One verb: its words and one target, action or verb, with op when nothing is named."},
	{Path: "verbs.*.words", In: SurfaceOmnibox, Kind: KindList, Elem: KindText, Required: true, Doc: "The words that name the verb in every language, in one list, such as [\"review\", \"revisar\"]: vocabulary, not a text a locale translates. Required."},
	{Path: "verbs.*.action", In: SurfaceOmnibox, Kind: KindText, Doc: "The catalog action it runs on each unit named."},
	{Path: "verbs.*.verb", In: SurfaceOmnibox, Kind: KindText, Doc: "The task verb of tasks.toml it runs on each task named; never one that resolves a gate."},
	{Path: "verbs.*.op", In: SurfaceOmnibox, Kind: KindEnum, Values: OmniboxOps, Doc: "What it runs on the text when it names no unit or task."},
}

// OmniboxOps are what an omnibox verb runs on text that names no unit or task.
func OmniboxOps() []Value {
	return []Value{{Name: "capture", Doc: "Captures a task titled with the text."}, {Name: "start", Doc: "Captures a task briefed with the text and starts it."}}
}

// GateHandlers are the task verb handlers that resolve or act on a gate, which no omnibox verb runs: a gate is
// resolved by the user on the gate itself.
var GateHandlers = []string{"approve", "answer", "edit", "retry", "drop"}

// GatedActions are the built-in actions that resolve a gate, post or discard unpublished work, which no
// omnibox verb runs: each is the user's on the draft, plan or workspace it acts on, behind its own
// confirmation.
var GatedActions = []string{"draft.edit", "draft.retry", "draft.close-silently", "draft.skip-workflow", "plan_apply", "plan_dismiss", "remove_force"}

// SafeActions are the other built-in actions, which an omnibox verb may run. Every built-in action is in
// exactly one of GatedActions and SafeActions.
var SafeActions = []string{"take_over", "pause", "resume", "mute", "unmute", "track", "untrack", "open", "remove",
	"brief_refresh", "intake_without", "intake_cancel", "plan_guide", "run.open", "run.enter", "run.transcript"}
