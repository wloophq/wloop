// Package readmodel keeps every unit's row derived, encoded and revised in
// memory, rebuilt from one store snapshot when a row input changes.
package readmodel

import (
	"context"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/domain/unit"
	intentload "github.com/wloophq/wloop/internal/intent"
)

// Entry is one unit's row as served: JSON is Row encoded, Rev the revision
// at which JSON last changed.
type Entry struct {
	Unit unit.Unit
	Row  api.Row
	JSON []byte
	Rev  uint64
}

// Provider is one provider's rows.
type Provider interface {
	Provider() unit.ProviderID
	// Listed are the units the provider serves a row for.
	Listed(ctx context.Context) ([]unit.Unit, error)
	// Load reads what the rows of units derive from; a unit missing from
	// the result has no row.
	Load(ctx context.Context, units []unit.Unit) (map[unit.Unit]any, error)
	// Derive is pure: the row of one unit from base and what Load read,
	// with the Link and Label Load read; the model sets the row's Unit.
	Derive(base Base, in any, at time.Time) (api.Row, error)
}

// Base is what every row of a rebuild shares, built once per rebuild.
type Base struct {
	Viewers map[unit.ProviderID]string
	Teams   []string // the github viewer's teams
	Specs   []api.ActionSpec
	// SpecsVersion is the version of GET /api/actions the rows' actions follow.
	SpecsVersion string
	// Intents are the loaded intents whose actions a row of a provider other than GitHub offers; nil offers
	// none.
	Intents *intentload.Loaded
	Pending map[unit.Unit]round.Pending
	Agents  []turns.AgentObservation
	// AgentsOK is false when the agents are unknown, so no session can be
	// told waiting.
	AgentsOK bool
	// Asked is the action a request asks for; zero for a served row.
	Asked catalog.Asked
}

// Summary is what any provider's row says in generic terms.
type Summary = api.Summary

// Snapshotter runs fn with every store read made with its ctx inside one
// read transaction.
type Snapshotter interface {
	Snapshot(ctx context.Context, fn func(ctx context.Context) error) error
}

// Pending is the set of pending automatic rounds.
type Pending interface {
	Pending(ctx context.Context) (map[unit.Unit]round.Pending, error)
}

// Agents is the agents' last known states; ok is false when they are
// unknown.
type Agents interface {
	Agents() (agents []turns.AgentObservation, ok bool)
}

// Settings supplies the Base fields no other port does: viewers, teams and
// action specs.
type Settings interface {
	Settings(ctx context.Context) (Base, error)
}
