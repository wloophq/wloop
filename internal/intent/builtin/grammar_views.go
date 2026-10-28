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
	{Path: "views.*", In: SurfaceViews, Kind: KindTable, Doc: "A view, by its name."},
	{Path: "views.*.label", In: SurfaceViews, Kind: KindText, Required: true, Doc: "The name people read in the list of views. Required."},
	{Path: "review", In: SurfaceViews, Kind: KindTable, Doc: "The review's steps, by step."},
	{Path: "review.*", In: SurfaceViews, Kind: KindTable, Doc: "A step of the review: inbox, outbox, waiting, projects, someday or focus."},
	{Path: "review.*.label", In: SurfaceViews, Kind: KindText, Required: true, Doc: "What the step asks of you, as the review lists it. Required."},
}
