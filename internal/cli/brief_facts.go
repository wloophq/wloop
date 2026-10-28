package cli

import (
	"context"
	"maps"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// briefedFacts adds the brief.* facts to the facts of every unit whose slot
// reads a brief: a pull request's and a Slack unit's own, and an entry's
// unit's, under the brief intent of the unit the brief is of.
type briefedFacts struct {
	store interface {
		UnitBrief(ctx context.Context, u unit.Unit) (inventory.UnitBrief, bool, error)
	}
	// intent is the brief intent of a provider's units.
	intent func(unit.ProviderID) brief.Intent
}

// taskUnitFact is the entry fact naming its unit as provider:key.
const taskUnitFact = "entry.unit"

func (b briefedFacts) add(ctx context.Context, u unit.Unit, f workflow.Facts) (workflow.Facts, error) {
	slot, ok := builtin.Slots[u.Provider]
	if !ok || !slices.Contains(builtin.BriefSubjects, slot) {
		return f, nil
	}
	of := u
	if raw, ok := f[taskUnitFact].(string); ok {
		p, key, found := strings.Cut(raw, ":")
		if !found {
			return withBrief(f, nil, b.intent(u.Provider)), nil
		}
		of = unit.Unit{Provider: unit.ProviderID(p), Key: key}
	}
	ub, ok, err := b.store.UnitBrief(ctx, of)
	if err != nil {
		return f, err
	}
	var stored *brief.Brief
	if ok {
		if stored, err = brief.Parse(ub.Brief); err != nil {
			stored = nil
		}
	}
	return withBrief(f, stored, b.intent(of.Provider)), nil
}

func withBrief(f workflow.Facts, b *brief.Brief, in brief.Intent) workflow.Facts {
	out := maps.Clone(f)
	if out == nil {
		out = workflow.Facts{}
	}
	maps.Copy(out, brief.Facts(b, in.Schema))
	return out
}
