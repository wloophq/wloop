// Package snapshot holds what a source plugin last reported of each of its units, as the host keeps it.
package snapshot

import (
	"context"
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
)

// Stored is a unit's last snapshot: the plugin's report as JSON, the rev the plugin gave it, and when the
// host received it.
type Stored struct {
	Unit unit.Unit
	Rev  uint64
	At   time.Time
	JSON []byte
}

// Store keeps the last snapshot of every unit a source plugin reports.
type Store interface {
	// Put replaces the snapshot of each of puts' units and drops gone's, in one write.
	Put(ctx context.Context, puts []Stored, gone []unit.Unit) error
	// Get is the snapshots of units; a unit with none is absent.
	Get(ctx context.Context, units []unit.Unit) (map[unit.Unit]Stored, error)
	// Revs are the revs of units' snapshots; a unit with none is absent.
	Revs(ctx context.Context, units []unit.Unit) (map[unit.Unit]uint64, error)
	// Units are p's units with a snapshot, by key.
	Units(ctx context.Context, p unit.ProviderID) ([]unit.Unit, error)
}
