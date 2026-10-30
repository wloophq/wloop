package source

import (
	"context"
	"fmt"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/plugins/source/value"
)

// UnitFacts are a source plugin's units as the automation engine reads them:
// the facts and the rev of each unit's last snapshot, read from the store
// and never asked of the plugin. A temporal value is a workflow.Temporal,
// which the engine resolves with its clock.
type UnitFacts struct {
	Snapshots *Snapshots
}

var (
	_ automation.Units      = UnitFacts{}
	_ automation.Revisioner = UnitFacts{}
)

// List are p's units with a snapshot.
func (f UnitFacts) List(ctx context.Context, p unit.ProviderID) ([]unit.Unit, error) {
	return f.Snapshots.Units(ctx, p)
}

// Facts are u's reported facts; automation.ErrUnitGone when u has no
// snapshot.
func (f UnitFacts) Facts(ctx context.Context, u unit.Unit) (workflow.Facts, error) {
	snap, ok, err := f.Snapshots.Last(ctx, u)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%s: %w", u, automation.ErrUnitGone)
	}
	out := make(workflow.Facts, len(snap.Facts))
	for _, fact := range snap.Facts {
		out[fact.Name] = value.Fact(fact.Value)
	}
	return out, nil
}

// Revs are the revs of units' last snapshots.
func (f UnitFacts) Revs(ctx context.Context, units []unit.Unit) (map[unit.Unit]uint64, error) {
	return f.Snapshots.Revs(ctx, units)
}
