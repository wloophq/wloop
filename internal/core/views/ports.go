package views

import (
	"context"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/view"
)

// The kinds of the events the views write; none is keyed.
const (
	KindViewApplied    = "view_applied"
	KindViewOverride   = "view_override"
	KindViewSinkFailed = "view_sink_failed"
)

// State is what every sink last received.
type State interface {
	Applied(ctx context.Context, sink string) (map[view.Key]view.Applied, error)
	// SaveApplied replaces the sink's row of a.Key().
	SaveApplied(ctx context.Context, sink string, a view.Applied, evs ...port.Event) error
	// SaveFailure stores the events of a write a sink refused.
	SaveFailure(ctx context.Context, evs ...port.Event) error
}

// Facts is what the views read of a pull request to project its author card.
type Facts interface {
	// Author returns the facts of u's pull request, with the checkout's when
	// needLocal; nil when u has none.
	Author(ctx context.Context, u unit.Unit, needLocal bool) (*view.AuthorFacts, error)
}

// Gates are the decisions waiting on the user; a unit's cards wait on them
// while one of its gates is open.
type Gates interface {
	Where(ctx context.Context, q gates.Query) ([]gate.Gate, error)
}
