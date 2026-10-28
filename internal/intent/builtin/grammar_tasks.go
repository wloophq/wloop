package builtin

import "github.com/wloophq/wloop/internal/domain/task"

// TaskVerbs are the verbs tasks.toml words, in the order clients list them.
var TaskVerbs = func() []string {
	out := make([]string, len(task.Verbs))
	for i, v := range task.Verbs {
		out[i] = string(v)
	}
	return out
}()

// TaskVerbInputs are the verbs that ask for a value before they run.
var TaskVerbInputs = []string{
	string(task.VerbWaiting), string(task.VerbSchedule), string(task.VerbProject), string(task.VerbContext),
	string(task.VerbArea), string(task.VerbDelegate), string(task.VerbReply),
}

// tasksGrammar is the keys of tasks.toml.
var tasksGrammar = []Key{
	{Path: "verbs", In: SurfaceTasks, Kind: KindTable, Doc: "The verbs a task offers, by verb."},
	{Path: "verbs.*", In: SurfaceTasks, Kind: KindTable, Doc: "A verb, by its name. A user layer may set only some keys of an entry declared below it; a new entry sets the required ones."},
	{Path: "verbs.*.label", In: SurfaceTasks, Kind: KindText, Required: true, Doc: "The name people read on its button. Required."},
	{Path: "verbs.*.summary", In: SurfaceTasks, Kind: KindText, Required: true, Doc: "One line, what pressing it does: its tooltip. Required."},
	{Path: "verbs.*.offer", In: SurfaceTasks, Kind: KindPred, Default: "true", Doc: "When a task offers the verb, over the task's own facts (task.id, task.kind, task.state, task.muted, task.unit, task.unit_provider); where it holds not, the verb is neither listed nor run."},
	{Path: "verbs.*.input", In: SurfaceTasks, Kind: KindText, Doc: "The question a verb that asks for a value puts first (waiting, schedule, project, context, area, delegate, reply); required for those."},
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
	{"focus", "Stars the task in Focus, or takes the star off."},
	{"schedule", "Hides the task until a date or until its unit moves, then brings it back where it was."},
	{"waiting", "Records who the task waits on and when it resurfaces."},
	{"approve", "Resolves the task's open gate as approved, or runs its unit's approve action."},
	{"delegate", "Starts a turn on the task, headless or in a session."},
	{"answer", "Answers the task's open question gate with the text it asks for."},
	{"edit", "Edits the text of the task's draft."},
	{"retry", "Sends again the task's approved draft whose delivery failed."},
	{"reply", "Posts a reply or a reaction on the task's unit; the composer's verb."},
	{"project", "Files the task under a parent, creating it from a title."},
	{"context", "Sets the task's contexts."},
	{"area", "Sets the task's area."},
	{"seen", "Marks the task seen; clients send it when it opens."},
	{"reviewed", "Marks a parent task reviewed."},
	{"sequential", "Switches a parent task's children between sequential and parallel."},
}

// HandlerArgs are the arguments a verb takes by its handler, which a
// task.verb step may pass; a verb with no handler, or another, takes none.
var HandlerArgs = map[string][]string{
	"schedule": {"until", "on_activity"},
	"waiting":  {"on", "resurface"},
	"context":  {"set"},
	"area":     {"set"},
}

func verbHandlerValues() []Value {
	out := make([]Value, len(VerbHandlers))
	for i, h := range VerbHandlers {
		out[i] = Value{Name: h.Name, Doc: h.Doc}
	}
	return out
}
