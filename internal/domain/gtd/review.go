package gtd

import "time"

// ReviewStep is one step of the weekly Review: the view whose entries it goes through.
// Its words are the profile's.
type ReviewStep struct {
	ID   string
	View View
}

// ReviewSteps are the Review's steps in order (spec 5.5). The projects step lists the review view: the projects
// without a next action or past their review interval.
var ReviewSteps = []ReviewStep{
	{ID: "inbox", View: ViewInbox},
	{ID: "outbox", View: ViewOutbox},
	{ID: "waiting", View: ViewWaiting},
	{ID: "projects", View: ViewReview},
	{ID: "someday", View: ViewSomeday},
	{ID: "focus", View: ViewFocus},
}

// ReviewRun is one Review: when it started, the steps done, and when the last of them was.
type ReviewRun struct {
	ID         string
	StartedAt  time.Time
	FinishedAt time.Time // zero while a step is open
	Done       []string  // step ids
}
