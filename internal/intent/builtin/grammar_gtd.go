package builtin

import "github.com/wloophq/wloop/internal/domain/gtd"

// GTDVerbs are the verbs gtd.toml words, in the order clients list them.
var GTDVerbs = []string{
	string(gtd.VerbNext), string(gtd.VerbWaiting), string(gtd.VerbSchedule), string(gtd.VerbSomeday),
	string(gtd.VerbReference), string(gtd.VerbFocus), string(gtd.VerbProject), string(gtd.VerbApprove),
	string(gtd.VerbDelegate), string(gtd.VerbDone), string(gtd.VerbDismiss), string(gtd.VerbSeen),
	string(gtd.VerbContext), string(gtd.VerbArea), string(gtd.VerbAnswer), string(gtd.VerbEdit),
	string(gtd.VerbRetry), string(gtd.VerbReviewed), string(gtd.VerbSequential), string(gtd.VerbReply),
}

// GTDViews are the views gtd.toml words, in the order clients list them;
// review is served only when its source is.
var GTDViews = []string{
	string(gtd.ViewFocus), string(gtd.ViewInbox), string(gtd.ViewOutbox), string(gtd.ViewNext),
	string(gtd.ViewWaiting), string(gtd.ViewProjects), string(gtd.ViewSomeday), string(gtd.ViewLogbook),
	string(gtd.ViewReview),
}

// gtdGrammar is the keys of gtd.toml.
var gtdGrammar = []Key{
	{Path: "verbs", In: SurfaceGTD, Kind: KindTable, Doc: "The verbs an entry offers, by verb."},
	{Path: "verbs.*", In: SurfaceGTD, Kind: KindTable, Doc: "A verb, by its name."},
	{Path: "verbs.*.label", In: SurfaceGTD, Kind: KindText, Required: true, Doc: "The name people read on its button. Required."},
	{Path: "verbs.*.summary", In: SurfaceGTD, Kind: KindText, Required: true, Doc: "One line, what pressing it does: its tooltip. Required."},
	{Path: "views", In: SurfaceGTD, Kind: KindTable, Doc: "The lists of entries, by view."},
	{Path: "views.*", In: SurfaceGTD, Kind: KindTable, Doc: "A view, by its name."},
	{Path: "views.*.label", In: SurfaceGTD, Kind: KindText, Required: true, Doc: "The name people read in the list of views. Required."},
}
