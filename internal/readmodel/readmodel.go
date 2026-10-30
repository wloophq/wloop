// Package readmodel keeps every unit's row derived, encoded and revised in
// memory, rebuilt from one store snapshot when a row input changes.
package readmodel

import (
	"context"
	"strings"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/unit"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/readmodel/overlay"
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
	// Viewers and Teams are the viewer's login and teams in each source, as
	// its plugin's Identity says.
	Viewers map[unit.ProviderID]string
	Teams   map[unit.ProviderID][]string
	Specs   []api.ActionSpec
	// SpecsVersion is the version of GET /api/actions the rows' actions follow.
	SpecsVersion string
	// Intents are the loaded intents whose actions a row of a provider other than GitHub offers; nil offers
	// none.
	Intents *intentload.Loaded
	// Plans are the automatic rounds the open plan gates hold, by unit.
	Plans  map[unit.Unit]overlay.Pending
	Agents []turns.AgentObservation
	// AgentsOK is false when the agents are unknown, so no session can be
	// told waiting.
	AgentsOK bool
	// Asked is the action a request asks for; zero for a served row.
	Asked catalog.Asked
}

// Identify sets the viewer and the teams of provider p from id, the
// Identity its plugin serves; a viewer whose key is not p's names no login.
func (b *Base) Identify(p unit.ProviderID, id src.Identity) {
	if b.Viewers == nil {
		b.Viewers = map[unit.ProviderID]string{}
	}
	if b.Teams == nil {
		b.Teams = map[unit.ProviderID][]string{}
	}
	b.Viewers[p], b.Teams[p] = Login(p, id), id.Teams
}

// Login is the viewer's login in provider p from id; "" when its key is
// not p's.
func Login(p unit.ProviderID, id src.Identity) string {
	login, ok := strings.CutPrefix(id.Viewer.Key, string(p)+":")
	if !ok {
		return ""
	}
	return login
}

// Summary is what any provider's row says in generic terms.
type Summary = api.Summary

// Snapshotter runs fn with every store read made with its ctx inside one
// read transaction.
type Snapshotter interface {
	Snapshot(ctx context.Context, fn func(ctx context.Context) error) error
}

// Plans are the automatic rounds the open plan gates hold, by unit.
type Plans interface {
	Plans(ctx context.Context) (map[unit.Unit]overlay.Pending, error)
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
