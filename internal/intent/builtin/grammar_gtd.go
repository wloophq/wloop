package builtin

import "github.com/wloophq/wloop/internal/domain/gtd"

// GTDVerbs are the verbs gtd.toml words, in the order clients list them.
var GTDVerbs = func() []string {
	out := make([]string, len(gtd.Verbs))
	for i, v := range gtd.Verbs {
		out[i] = string(v)
	}
	return out
}()

// GTDVerbInputs are the verbs that ask for a value before they run.
var GTDVerbInputs = []string{
	string(gtd.VerbWaiting), string(gtd.VerbSchedule), string(gtd.VerbProject), string(gtd.VerbContext),
	string(gtd.VerbArea), string(gtd.VerbDelegate), string(gtd.VerbReply),
}

// GTDViews are the views gtd.toml words, in the order clients list them;
// review is served only when its source is.
var GTDViews = []string{
	string(gtd.ViewFocus), string(gtd.ViewInbox), string(gtd.ViewOutbox), string(gtd.ViewNext),
	string(gtd.ViewWaiting), string(gtd.ViewProjects), string(gtd.ViewSomeday), string(gtd.ViewLogbook),
	string(gtd.ViewReview),
}

// GTDReviewSteps are the weekly Review's steps, in order.
var GTDReviewSteps = func() []string {
	out := make([]string, len(gtd.ReviewSteps))
	for i, st := range gtd.ReviewSteps {
		out[i] = st.ID
	}
	return out
}()

// gtdGrammar is the keys of gtd.toml.
var gtdGrammar = []Key{
	{Path: "verbs", In: SurfaceGTD, Kind: KindTable, Doc: "The verbs an entry offers, by verb."},
	{Path: "verbs.*", In: SurfaceGTD, Kind: KindTable, Doc: "A verb, by its name."},
	{Path: "verbs.*.label", In: SurfaceGTD, Kind: KindText, Required: true, Doc: "The name people read on its button. Required."},
	{Path: "verbs.*.summary", In: SurfaceGTD, Kind: KindText, Required: true, Doc: "One line, what pressing it does: its tooltip. Required."},
	{Path: "verbs.*.input", In: SurfaceGTD, Kind: KindText, Doc: "The question a verb that asks for a value puts first (waiting, schedule, project, context, area, delegate, reply); required for those."},
	{Path: "views", In: SurfaceGTD, Kind: KindTable, Doc: "The lists of entries, by view."},
	{Path: "views.*", In: SurfaceGTD, Kind: KindTable, Doc: "A view, by its name."},
	{Path: "views.*.label", In: SurfaceGTD, Kind: KindText, Required: true, Doc: "The name people read in the list of views. Required."},
	{Path: "review", In: SurfaceGTD, Kind: KindTable, Doc: "The weekly Review's steps, by step."},
	{Path: "review.*", In: SurfaceGTD, Kind: KindTable, Doc: "A step of the Review: inbox, outbox, waiting, projects, someday or focus."},
	{Path: "review.*.label", In: SurfaceGTD, Kind: KindText, Required: true, Doc: "What the step asks of you, as the Review lists it. Required."},
}

// keysGrammar is the keys of keys.toml.
var keysGrammar = []Key{
	{Path: "help", In: SurfaceKeys, Kind: KindTable, Doc: "The help line of each core key, by context (list, global, question, review, plan), then action (`nav:inbox`, `done`); a numbered one as `answer:{n}`, `{n}` its number."},
	{Path: "help.*", In: SurfaceKeys, Kind: KindTable, Doc: "The keys of one context, by action."},
	{Path: "help.*.*", In: SurfaceKeys, Kind: KindText, Required: true, Doc: "What the key does, as the help and the palette list it. Required."},
}
