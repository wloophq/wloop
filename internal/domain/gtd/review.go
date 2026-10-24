package gtd

import "time"

// ReviewStep is one step of the weekly Review: the view whose entries it goes through.
type ReviewStep struct {
	ID    string
	Label string
	View  View
}

// ReviewSteps are the Review's steps in order (spec 5.5). The projects step lists the review view: the projects
// without a next action or past their review interval.
var ReviewSteps = []ReviewStep{
	{ID: "inbox", Label: "Inbox to zero", View: ViewInbox},
	{ID: "outbox", Label: "Outbox", View: ViewOutbox},
	{ID: "waiting", Label: "Waiting: nudge?", View: ViewWaiting},
	{ID: "projects", Label: "Projects without a next action or past their review", View: ViewReview},
	{ID: "someday", Label: "Someday", View: ViewSomeday},
	{ID: "focus", Label: "Prune Focus", View: ViewFocus},
}

// ReviewRun is one Review: when it started, the steps done, and when the last of them was.
type ReviewRun struct {
	ID         string
	StartedAt  time.Time
	FinishedAt time.Time // zero while a step is open
	Done       []string  // step ids
}
