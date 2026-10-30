package api

import (
	"net/url"
	"time"
)

// The Review's routes: ReviewPath serves the current Review, ReviewStepDone marks one of its steps done.
const (
	ReviewPath     = "/api/review"
	ReviewStepDone = ReviewPath + "/steps/{step}/done"
)

// ReviewStepPath is the path that marks step done.
func ReviewStepPath(step string) string {
	return ReviewPath + "/steps/" + url.PathEscape(step) + "/done"
}

// Review is the Review. ID is empty when nothing was left to review as it started.
type Review struct {
	ID        string       `json:"id"`
	Steps     []ReviewStep `json:"steps"`
	StartedAt time.Time    `json:"started_at"`
	Done      bool         `json:"done"`
}

// ReviewStep is a step of the Review: the tasks of Query (GET /api/tasks with these parameters), their
// count, and whether the step is done. Step ids: inbox, outbox, waiting, projects, someday, focus.
type ReviewStep struct {
	ID    string            `json:"id"`
	Label string            `json:"label"`
	View  string            `json:"view"`
	Query map[string]string `json:"query"`
	Count int               `json:"count"`
	Done  bool              `json:"done"`
}
