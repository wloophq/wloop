package inventory

import (
	"context"
	"time"

	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// ReviewTurnsFollowed bounds how long after its newest review turn a pull
// request without a review workspace is still read for a follow-up.
const ReviewTurnsFollowed = 30 * 24 * time.Hour

// PlanInputs are what a plan reads of the stored pull requests.
type PlanInputs interface {
	// Viewer is the user's stored GitHub login; "" until the collector
	// stores it.
	Viewer(ctx context.Context) (string, error)
	// Rows are every stored pull request, open or not.
	Rows(ctx context.Context) ([]PR, error)
	// Facts are the facts of rows, each from its row and its stored full
	// read; viewer sets ByViewer and ViewerRequested.
	Facts(ctx context.Context, rows []PR, viewer string) (map[unit.Unit]PlanFacts, error)
	// Ended tells which of units have an event of kind.
	Ended(ctx context.Context, kind string, units []unit.Unit) (map[unit.Unit]bool, error)
}

// PlanFacts are a pull request's facts and when the full read they carry was
// taken: zero without one this release reads.
type PlanFacts struct {
	pr.Facts
	ReadAt time.Time
}
