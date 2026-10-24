// Package views projects every pull request's item view to the sinks that
// show it.
package views

import (
	"context"

	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/view"
)

type ViewSink interface {
	Name() string
	// Manages reports whether column is in the vocabulary the sink manages
	// for cards of role; outside it the user's placement wins.
	Manages(role view.Role, column string) bool
	// Apply writes each item's column when force holds its unit, its
	// card is in a managed column or in none; otherwise only the summary and
	// unread, with Applied.Override set to the user's column. An item with no
	// status is not written.
	Apply(ctx context.Context, items []view.Item, force map[unit.Unit]bool) ([]view.Applied, error)
}

type PlacementReader interface {
	// Placements is the column the user sees for each unit's card.
	Placements(ctx context.Context) (map[unit.Unit]string, error)
}

// StatusReader is a sink that can say which status a column of its board is,
// None when the column is not one it writes for role.
type StatusReader interface {
	Status(role view.Role, column string) view.Status
}
