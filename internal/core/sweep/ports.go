// Package sweep holds the inputs a sweep plans from, as ports: the stored
// pull requests, the workspace snapshot, the turn books, the round state and
// the local checkouts.
package sweep

import (
	"context"
	"time"

	"github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workspace"
)

// ReviewTurnsFollowed bounds how long after its newest review turn a pull
// request without a review workspace is still read for a follow-up.
const ReviewTurnsFollowed = 30 * 24 * time.Hour

// Inputs are what a plan reads of the stored pull requests.
type Inputs interface {
	// Viewer is the user's stored GitHub login; "" until the collector
	// stores it.
	Viewer(ctx context.Context) (string, error)
	// Rows are every stored pull request, open or not.
	Rows(ctx context.Context) ([]inventory.PR, error)
	// Facts are the facts of rows, each from its row and its stored full
	// read; viewer sets ByViewer and ViewerRequested.
	Facts(ctx context.Context, rows []inventory.PR, viewer string) (map[unit.Unit]Facts, error)
	// Ended tells which of units have an event of kind.
	Ended(ctx context.Context, kind string, units []unit.Unit) (map[unit.Unit]bool, error)
}

// Facts are a pull request's facts and when the full read they carry was
// taken: zero without one this release reads.
type Facts struct {
	pr.Facts
	ReadAt time.Time
}

// Snapshot is the workspace manager's last snapshot.
type Snapshot interface {
	Read(ctx context.Context, want workspace.Part) (*workspace.Snapshot, error)
}

type Books interface {
	Books(ctx context.Context, units ...unit.Unit) (map[unit.Unit]turn.Book, error)
}

// Rounds are the round state and the pause.
type Rounds interface {
	Load(ctx context.Context) (*round.Store, error)
	Pause(ctx context.Context) (round.Pause, error)
}

// GitFacts are what a plan reads of a checkout's state.
type GitFacts struct {
	Dirty bool // a tracked file changed
	Head  string
}

// LocalGit reads the local checkouts.
type LocalGit interface {
	Status(ctx context.Context, dir string) (GitFacts, error)
	Head(ctx context.Context, dir string) (string, error)
	// Upstream is the remote branch the checkout's branch tracks; "" when none.
	Upstream(ctx context.Context, dir string) (string, error)
	IsAncestor(ctx context.Context, dir, ancestor, commit string) (bool, error)
}
