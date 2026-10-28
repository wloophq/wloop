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
}
