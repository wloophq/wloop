package builtin

import "github.com/wloophq/wloop/internal/domain/task"

// Views are the views views.toml words, in the order clients list them;
// review is served only when its source is.
var Views = []string{
	string(task.ViewFocus), string(task.ViewInbox), string(task.ViewOutbox), string(task.ViewNext),
	string(task.ViewWaiting), string(task.ViewProjects), string(task.ViewSomeday), string(task.ViewLogbook),
	string(task.ViewReview),
}

// ReviewSteps are the review's steps, in order.
var ReviewSteps = func() []string {
	out := make([]string, len(task.ReviewSteps))
	for i, st := range task.ReviewSteps {
		out[i] = st.ID
	}
	return out
}()

// viewsGrammar is the keys of views.toml.
var viewsGrammar = []Key{
	{Path: "views", In: SurfaceViews, Kind: KindTable, Doc: "The lists of tasks, by view."},
	{Path: "views.*", In: SurfaceViews, Kind: KindTable, Doc: "A view, by its name. A user layer may set only some keys of an entry declared below it; a new entry sets the required ones."},
	{Path: "views.*.label", In: SurfaceViews, Kind: KindText, Required: true, Doc: "The name people read in the list of views. Required."},
	{Path: "views.*.over", In: SurfaceViews, Kind: KindEnum, Values: named("tasks", "gates"), Default: "tasks", Doc: "What the view lists: tasks, or gates."},
	{Path: "views.*.when", In: SurfaceViews, Kind: KindPred, Doc: "Which tasks or gates the view lists, over their facts."},
	{Path: "views.*.group", In: SurfaceViews, Kind: KindText, Doc: "The fact the view groups by."},
	{Path: "views.*.sort", In: SurfaceViews, Kind: KindText, Doc: "The facts the view sorts by, comma-separated; a leading - sorts one descending."},
	{Path: "views.*.key", In: SurfaceViews, Kind: KindText, Doc: "The key that jumps to the view, in internal/keys' notation."},
	{Path: "views.*.hide_disabled", In: SurfaceViews, Kind: KindBool, Default: "false", Doc: "The view hides the tasks of a disabled plugin."},
	{Path: "badge", In: SurfaceViews, Kind: KindTable, Doc: "The menu bar's badge."},
	{Path: "badge.views", In: SurfaceViews, Kind: KindList, Elem: KindText, Doc: "The views whose counts the badge adds up."},
	{Path: "review", In: SurfaceViews, Kind: KindTable, Doc: "The review's steps, by step."},
	{Path: "review.*", In: SurfaceViews, Kind: KindTable, Doc: "A step of the review: inbox, outbox, waiting, projects, someday or focus. A user layer may set only some keys of an entry declared below it; a new entry sets the required ones."},
	{Path: "review.*.label", In: SurfaceViews, Kind: KindText, Required: true, Doc: "What the step asks of you, as the review lists it. Required."},
}
