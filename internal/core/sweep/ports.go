// Package sweep holds the inputs a sweep plans from, as ports: the
// workspace snapshot, the turn books, the round state and the local
// checkouts.
package sweep

import (
	"context"

	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workspace"
)

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
