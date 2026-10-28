package builtin

import "github.com/wloophq/wloop/internal/domain/task"

// TaskVerbs are the verbs gtd.toml words, in the order clients list them.
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

// Views are the views gtd.toml words, in the order clients list them;
// review is served only when its source is.
var Views = []string{
	string(task.ViewFocus), string(task.ViewInbox), string(task.ViewOutbox), string(task.ViewNext),
	string(task.ViewWaiting), string(task.ViewProjects), string(task.ViewSomeday), string(task.ViewLogbook),
	string(task.ViewReview),
}

// ReviewSteps are the weekly Review's steps, in order.
var ReviewSteps = func() []string {
	out := make([]string, len(task.ReviewSteps))
	for i, st := range task.ReviewSteps {
		out[i] = st.ID
	}
	return out
}()

// listsGrammar is the keys of gtd.toml.
var listsGrammar = []Key{
	{Path: "verbs", In: SurfaceViews, Kind: KindTable, Doc: "The verbs an entry offers, by verb."},
	{Path: "verbs.*", In: SurfaceViews, Kind: KindTable, Doc: "A verb, by its name."},
	{Path: "verbs.*.label", In: SurfaceViews, Kind: KindText, Required: true, Doc: "The name people read on its button. Required."},
	{Path: "verbs.*.summary", In: SurfaceViews, Kind: KindText, Required: true, Doc: "One line, what pressing it does: its tooltip. Required."},
	{Path: "verbs.*.offer", In: SurfaceViews, Kind: KindPred, Default: "true", Doc: "When an entry offers the verb, over the entry's own facts (entry.id, entry.kind, entry.state, entry.muted, entry.unit, entry.unit_provider); where it holds not, the verb is neither listed nor run."},
	{Path: "verbs.*.input", In: SurfaceViews, Kind: KindText, Doc: "The question a verb that asks for a value puts first (waiting, schedule, project, context, area, delegate, reply); required for those."},
	{Path: "views", In: SurfaceViews, Kind: KindTable, Doc: "The lists of entries, by view."},
	{Path: "views.*", In: SurfaceViews, Kind: KindTable, Doc: "A view, by its name."},
	{Path: "views.*.label", In: SurfaceViews, Kind: KindText, Required: true, Doc: "The name people read in the list of views. Required."},
	{Path: "review", In: SurfaceViews, Kind: KindTable, Doc: "The weekly Review's steps, by step."},
	{Path: "review.*", In: SurfaceViews, Kind: KindTable, Doc: "A step of the Review: inbox, outbox, waiting, projects, someday or focus."},
	{Path: "review.*.label", In: SurfaceViews, Kind: KindText, Required: true, Doc: "What the step asks of you, as the Review lists it. Required."},
}

// keysGrammar is the keys of keys.toml.
var keysGrammar = []Key{
	{Path: "help", In: SurfaceKeys, Kind: KindTable, Doc: "The help line of each core key, by context (list, global, question, review, plan), then action (`nav:inbox`, `done`); a numbered one as `answer:{n}`, `{n}` its number."},
	{Path: "help.*", In: SurfaceKeys, Kind: KindTable, Doc: "The keys of one context, by action."},
	{Path: "help.*.*", In: SurfaceKeys, Kind: KindText, Required: true, Doc: "What the key does, as the help and the palette list it. Required."},
}
