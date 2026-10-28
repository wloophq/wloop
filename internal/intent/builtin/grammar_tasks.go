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
	{Path: "verbs.*", In: SurfaceTasks, Kind: KindTable, Doc: "A verb, by its name."},
	{Path: "verbs.*.label", In: SurfaceTasks, Kind: KindText, Required: true, Doc: "The name people read on its button. Required."},
	{Path: "verbs.*.summary", In: SurfaceTasks, Kind: KindText, Required: true, Doc: "One line, what pressing it does: its tooltip. Required."},
	{Path: "verbs.*.offer", In: SurfaceTasks, Kind: KindPred, Default: "true", Doc: "When a task offers the verb, over the task's own facts (task.id, task.kind, task.state, task.muted, task.unit, task.unit_provider); where it holds not, the verb is neither listed nor run."},
	{Path: "verbs.*.input", In: SurfaceTasks, Kind: KindText, Doc: "The question a verb that asks for a value puts first (waiting, schedule, project, context, area, delegate, reply); required for those."},
	{Path: "verbs.*.from", In: SurfaceTasks, Kind: KindList, Elem: KindText, Doc: "The states the verb moves a task from; every state when absent."},
	{Path: "verbs.*.to", In: SurfaceTasks, Kind: KindText, Doc: "The state the verb moves a task to; absent, the verb moves none."},
	{Path: "verbs.*.by", In: SurfaceTasks, Kind: KindMap, Doc: "Who may run the verb, by actor (person, workflow, agent, plugin); an actor left out may not."},
	{Path: "verbs.*.by.*", In: SurfaceTasks, Kind: KindText, Doc: "A predicate over the task's facts that lets the actor run the verb where it holds, or \"gate\", which opens a proposal gate a person approves."},
	{Path: "verbs.*.send", In: SurfaceTasks, Kind: KindEnum, Values: named("now", "draft"), Default: "draft", Doc: "With reply: now sends the reply at once, draft proposes it in the Outbox."},
	{Path: "props", In: SurfaceTasks, Kind: KindMap, Doc: "The task properties, by name; each is the fact task.prop.<name>."},
	{Path: "props.*", In: SurfaceTasks, Kind: KindTable, Doc: "A property, by its name."},
	{Path: "props.*.type", In: SurfaceTasks, Kind: KindEnum, Values: propTypeValues, Required: true, Doc: "The property's type. Required."},
	{Path: "props.*.values", In: SurfaceTasks, Kind: KindList, Elem: KindText, Doc: "An enum's values, in order."},
	{Path: "props.*.writers", In: SurfaceTasks, Kind: KindList, Elem: KindEnum, Values: writerValues, Doc: "Who may write the property; one an agent or a plugin may write is untrusted, so no approval rule reads it."},
	{Path: "props.*.doc", In: SurfaceTasks, Kind: KindText, Doc: "What the property holds, for the facts' reference and the agents."},
	{Path: "states", In: SurfaceTasks, Kind: KindMap, Doc: "The states a task takes, by name, in the order clients list them."},
	{Path: "states.*", In: SurfaceTasks, Kind: KindTable, Doc: "A state, by its name."},
	{Path: "states.*.category", In: SurfaceTasks, Kind: KindEnum, Values: categoryValues, Required: true, Doc: "The state's category, which is all Go reads of it. Required."},
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
