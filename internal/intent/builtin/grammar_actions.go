package builtin

// GroupStart is the action group of the actions that start or continue a
// unit's turn, the intents' default.
const GroupStart = "start"

// ActionGroups are the groups an action is listed under.
var ActionGroups = []string{GroupStart, "turn", "control", "workspace", "brief", "intake"}

// ActionSurfaces are where an action may be offered.
var ActionSurfaces = []string{"row", "detail", "tui", "menubar", "notification", "cli", "explore"}

// BuiltinActionIDs are the built-in actions actions.toml words, one table
// each: a row's, a draft's, a plan item's and a task run's. plan_guide is the plan item's
// guide, whose wire id is guide.
var BuiltinActionIDs = []string{
	"run_now", "skip", "guide", "trust_run", "unblock",
	"answer", "continue", "dismiss",
	"take_over", "pause", "resume", "mute", "unmute", "track", "untrack",
	"open", "remove", "remove_force",
	"brief_refresh", "intake_without", "intake_cancel",
	"draft.edit", "draft.retry",
	"plan_apply", "plan_dismiss", "plan_guide",
	"run.open", "run.enter", "run.transcript",
}

func actionValues(names []string) func() []Value {
	return func() []Value {
		out := make([]Value, len(names))
		for i, n := range names {
			out[i] = Value{Name: n}
		}
		return out
	}
}

// actionsGrammar is the keys of an intent's [action] and of actions.toml.
var actionsGrammar = []Key{
	{Path: "action", In: SurfaceIntent, Kind: KindTable, Doc: "How the intent's action is offered on a row; its description is the intent's summary."},
	{Path: "action.group", In: SurfaceIntent, Kind: KindEnum, Values: actionValues(ActionGroups), Default: GroupStart, Doc: "The group the action is listed under."},
	{Path: "action.surfaces", In: SurfaceIntent, Kind: KindList, Elem: KindEnum, Values: actionValues(ActionSurfaces), Doc: "Where the action is offered: row, detail, tui, cli and explore when absent."},
	{Path: "action.when", In: SurfaceIntent, Kind: KindPred, Default: "row.open", Doc: "When the action is offered on a row."},
	{Path: "action.primary", In: SurfaceIntent, Kind: KindTable, Doc: "When the action is the row's primary one."},
	{Path: "action.primary.when", In: SurfaceIntent, Kind: KindPred, Default: "false", Doc: "When the action is the row's primary one."},
	{Path: "action.refuse[]", In: SurfaceIntent, Kind: KindTable, Doc: "A case that offers the action disabled; the first that holds says why."},
	{Path: "action.refuse[].when", In: SurfaceIntent, Kind: KindPred, Doc: "When the action is offered disabled."},
	{Path: "action.refuse[].why", In: SurfaceIntent, Kind: KindTemplate, Doc: "Why the action is disabled, over the action facts."},
	{Path: "action.note", In: SurfaceIntent, Kind: KindTemplate, Doc: "What the action would do on this row, over the action facts."},
	{Path: "action.warning", In: SurfaceIntent, Kind: KindTemplate, Doc: "Shown under the action, over the action facts; empty shows nothing."},
	{Path: "action.confirm", In: SurfaceIntent, Kind: KindTemplate, Doc: "The dialog that confirms the action, over the action facts; empty asks nothing."},

	{Path: "*", In: SurfaceActions, Kind: KindTable, Doc: "A built-in action, by its id."},
	{Path: "*.label", In: SurfaceActions, Kind: KindText, Required: true, Doc: "The name people read. Required."},
	{Path: "*.summary", In: SurfaceActions, Kind: KindText, Required: true, Doc: "One line, what pressing it does: its tooltip and help line. Required."},
	{Path: "*.note", In: SurfaceActions, Kind: KindTemplate, Doc: "What the action would do on this row, over the action facts."},
	{Path: "*.warning", In: SurfaceActions, Kind: KindTemplate, Doc: "Shown under the action, over the action facts; empty shows nothing."},
	{Path: "*.confirm", In: SurfaceActions, Kind: KindTemplate, Doc: "The dialog that confirms the action, over the action facts; empty asks nothing."},
	{Path: "*.why", In: SurfaceActions, Kind: KindTemplate, Doc: "Why the action is disabled, over the action facts."},
	{Path: "*.input", In: SurfaceActions, Kind: KindTable, Doc: "The text the action asks for."},
	{Path: "*.input.label", In: SurfaceActions, Kind: KindText, Doc: "The input's name."},
	{Path: "*.input.help", In: SurfaceActions, Kind: KindText, Doc: "What to write, shown in the empty input."},
}
