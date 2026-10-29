package builtin

import "github.com/wloophq/wloop/internal/domain/task"

// tasksGrammar is the keys of tasks.toml.
var tasksGrammar = []Key{
	{Path: "verbs", In: SurfaceTasks, Kind: KindTable, Doc: "The verbs a task offers, by verb."},
	{Path: "verbs.*", In: SurfaceTasks, Kind: KindTable, Doc: "A verb, by its name: lowercase letters, digits and hyphens, starting with a letter. A user layer may set only some keys of an entry declared below it, and adds its own verbs, which record the event verb_<name>; a new entry sets the required ones."},
	{Path: "verbs.*.label", In: SurfaceTasks, Kind: KindText, Required: true, Doc: "The name people read on its button. Required."},
	{Path: "verbs.*.summary", In: SurfaceTasks, Kind: KindText, Required: true, Doc: "One line, what pressing it does: its tooltip. Required."},
	{Path: "verbs.*.offer", In: SurfaceTasks, Kind: KindPred, Default: "true", Doc: "When a task offers the verb, over the task's own facts (task.id, task.prop.kind, task.state, task.unit, task.unit_provider); where it holds not, the verb is neither listed nor run."},
	{Path: "verbs.*.input", In: SurfaceTasks, Kind: KindText, Doc: "The question a verb that asks for a value puts first; required for a verb whose handler asks for one (waiting, schedule, project, context, area, delegate, reply, promise)."},
	{Path: "verbs.*.from", In: SurfaceTasks, Kind: KindList, Elem: KindText, Doc: "The states the verb moves a task from, each a state of states; every state when absent."},
	{Path: "verbs.*.to", In: SurfaceTasks, Kind: KindText, Doc: "The state the verb moves a task to, a state of states; absent, the verb moves none."},
	{Path: "verbs.*.by", In: SurfaceTasks, Kind: KindMap, Doc: "Who may run the verb, by actor (person, workflow, agent, plugin); an actor left out may not."},
	{Path: "verbs.*.by.*", In: SurfaceTasks, Kind: KindText, Doc: "A predicate over the task's facts that lets the actor run the verb where it holds, or \"gate\", which opens the gate the verb's gate names, and a person's approval runs the verb as theirs; true lets the actor run it anywhere."},
	{Path: "verbs.*.gate", In: SurfaceTasks, Kind: KindText, Doc: "The gate, of gates.toml, a by.<actor> of \"gate\" opens; required when one does."},
	{Path: "verbs.*.handler", In: SurfaceTasks, Kind: KindEnum, Values: verbHandlerValues, Doc: "What the verb runs besides its move, a handler of the loop's; none when absent."},
	{Path: "verbs.*.send", In: SurfaceTasks, Kind: KindEnum, Values: named("now", "draft"), Default: "draft", Doc: "On a verb whose handler is reply: what a reply that names no send does. now sends it at once, as the person approving it; draft holds it on its gate for approval. The composer's Save as draft always holds it."},
	{Path: "props", In: SurfaceTasks, Kind: KindMap, Doc: "The task properties, by name, in the order the file declares them, a user layer's new ones after the bundled; each is the fact task.prop.<name>, typed from its type."},
	{Path: "props.*", In: SurfaceTasks, Kind: KindTable, Doc: "A property, by its name. A user layer may set only some keys of an entry declared below it; a new entry sets the required ones."},
	{Path: "props.*.type", In: SurfaceTasks, Kind: KindEnum, Values: propTypeValues, Required: true, Doc: "The property's type; required for a new property. An int or a duration of 0 means unset: a value of 0 is refused."},
	{Path: "props.*.values", In: SurfaceTasks, Kind: KindList, Elem: KindText, Doc: "An enum's values, in order; required with type enum."},
	{Path: "props.*.writers", In: SurfaceTasks, Kind: KindList, Elem: KindEnum, Values: writerValues, Doc: "Who may write the property; one an agent or a plugin may write is untrusted, so no approval rule reads it."},
	{Path: "props.*.doc", In: SurfaceTasks, Kind: KindText, Doc: "What the property holds, for the facts' reference and the agents."},
	{Path: "props.*.role", In: SurfaceTasks, Kind: KindEnum, Values: propRoleValues, Doc: "The mechanism of the loop that acts on the property; at most one property takes each role. Go names the role, never the property."},
	{Path: "states", In: SurfaceTasks, Kind: KindMap, Doc: "The states a task takes, by name, in the order the file declares them, a user layer's new ones after the bundled; clients list them in this order, and a task proposed in a category takes the first state of it."},
	{Path: "states.*", In: SurfaceTasks, Kind: KindTable, Doc: "A state, by its name. A user layer may set only some keys of an entry declared below it; a new entry sets the required ones."},
	{Path: "states.*.category", In: SurfaceTasks, Kind: KindEnum, Values: categoryValues, Required: true, Doc: "The state's category, which is all Go reads of it; required for a new state."},
	{Path: "states.*.label", In: SurfaceTasks, Kind: KindText, Doc: "The name people read."},
	{Path: "owed_by", In: SurfaceTasks, Kind: KindTable, Doc: "Who owes a task's next step."},
	{Path: "owed_by.expr", In: SurfaceTasks, Kind: KindText, Doc: "An expression over the task's facts whose value, me or a person's key, is task.owed_by."},
	{Path: "chat", In: SurfaceTasks, Kind: KindTable, Doc: "The chat on a task."},
	{Path: "chat.intent", In: SurfaceTasks, Kind: KindText, Doc: "The intent of the turn a message starts on a task with no live session."},
	{Path: "areas", In: SurfaceTasks, Kind: KindList, Elem: KindText, Doc: "The areas a task may be in, the values of the area property."},
	{Path: "contexts", In: SurfaceTasks, Kind: KindList, Elem: KindText, Doc: "The contexts the menu offers; a task may hold others."},
	{Path: "stale_waiting_days", In: SurfaceTasks, Kind: KindInt, Doc: "Working days a task waits before it is stale."},
}

func propTypeValues() []Value {
	out := make([]Value, len(task.PropTypes))
	for i, t := range task.PropTypes {
		out[i] = Value{Name: string(t)}
	}
	return out
}

// RoleDocs say what each role does with the property that takes it.
var RoleDocs = map[task.Role]string{
	task.RoleArea:     "A string: the area filter of the lists and the counters reads it, the area handler sets it, and a new parent takes it from the task it starts with.",
	task.RoleContexts: "A string[]: the contexts filter reads it, the context handler sets it, and a task takes it from the item or the request that opens it.",
	task.RoleStar:     "An enum: the star handler toggles it between its first value, starred, and its second.",
	task.RoleDeadline: "A date: a task takes it from its items' deadline, and a draft's task from its gate's window.",
	task.RolePromise:  "A date: the promise handler sets it to the time you promise the task by, and task.promised reads it.",
}

func propRoleValues() []Value {
	out := make([]Value, len(task.Roles))
	for i, r := range task.Roles {
		out[i] = Value{Name: string(r), Doc: RoleDocs[r]}
	}
	return out
}

func categoryValues() []Value {
	out := make([]Value, len(task.Categories))
	for i, c := range task.Categories {
		out[i] = Value{Name: string(c)}
	}
	return out
}

// VerbHandler is a handler a verb of tasks.toml may run besides its move.
type VerbHandler struct{ Name, Doc string }

// VerbHandlers are the handlers a verb names with handler.
var VerbHandlers = []VerbHandler{
	{"finish", "Moves the task to the verb's to; a project's dossier starts its lifecycle."},
	{"drop", "Moves the task to the verb's to; a kind another service serves drops its subject: a draft is discarded, a message's gate refused."},
	{"focus", "Stars the task, or takes the star off: toggles the property of role star."},
	{"schedule", "Hides the task until a date or until its unit moves, then brings it back where it was."},
	{"wake", "Brings a deferred task back where its wake returns it, the first ready state when it names none, and drops the wake."},
	{"waiting", "Records who the task waits on and when it resurfaces."},
	{"approve", "Resolves the task's open gate as approved, or runs its unit's approve action."},
	{"delegate", "Starts a turn on the task, headless or in a session."},
	{"answer", "Answers the task's open question gate with the text it asks for."},
	{"edit", "Edits the text of the task's draft."},
	{"retry", "Sends again the task's approved draft whose delivery failed."},
	{"reply", "Posts a reply or a reaction on the task's unit; the composer's verb."},
	{"project", "Files the task under a parent, creating it from a title."},
	{"context", "Sets the task's contexts: the property of role contexts."},
	{"area", "Sets the task's area: the property of role area."},
	{"seen", "Marks the task seen; clients send it when it opens."},
	{"promise", "Sets the property of role promise to the time its by names: HH:MM, today or tomorrow once that passed; YYYY-MM-DD, 18:00 that day; or an RFC 3339 time. An empty by clears it."},
	{"reviewed", "Marks a parent task reviewed."},
	{"sequential", "Switches a parent task's children between sequential and parallel."},
}

// HandlerArgs are the arguments a verb takes by its handler, which a
// task.verb step may pass; a verb with no handler, or another, takes none.
var HandlerArgs = map[string][]string{
	"promise":  {"by"},
	"schedule": {"until", "on_activity"},
	"waiting":  {"on", "resurface"},
	"context":  {"set"},
	"area":     {"set"},
}

// InputHandlers are the handlers that ask for a value before they run: a
// verb that names one puts its question in input.
var InputHandlers = []string{"waiting", "schedule", "project", "context", "area", "delegate", "reply", "promise"}

func verbHandlerValues() []Value {
	out := make([]Value, len(VerbHandlers))
	for i, h := range VerbHandlers {
		out[i] = Value{Name: h.Name, Doc: h.Doc}
	}
	return out
}
