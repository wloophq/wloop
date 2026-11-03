package cli

import (
	"context"
	"maps"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/brief"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// briefedFacts adds the brief.* facts to the facts of every unit whose slot
// reads a brief: a pull request's and a Slack unit's own, and a task's
// unit's, under the brief intent of the unit the brief is of.
type briefedFacts struct {
	store interface {
		UnitBrief(ctx context.Context, u unit.Unit) (inventory.UnitBrief, bool, error)
	}
	// intent is the brief intent of a provider's units.
	intent func(unit.ProviderID) brief.Intent
}

// taskUnitFact is the task fact naming its unit as provider:key.
const taskUnitFact = "task.unit"

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
	ub, ok, err := b.brief(ctx, of)
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

// unitBriefsReader is an optional capability of a briefedFacts store: the briefs of units, which a pass reads once
// for every unit of it.
type unitBriefsReader interface {
	UnitBriefs(ctx context.Context, units []unit.Unit) (map[unit.Unit]inventory.UnitBrief, error)
}

func (b briefedFacts) brief(ctx context.Context, u unit.Unit) (inventory.UnitBrief, bool, error) {
	var all func([]unit.Unit) (map[unit.Unit]inventory.UnitBrief, error)
	if r, ok := b.store.(unitBriefsReader); ok {
		all = func(units []unit.Unit) (map[unit.Unit]inventory.UnitBrief, error) { return r.UnitBriefs(ctx, units) }
	}
	return coretasks.UnitLookup(ctx, "cli.briefs", u, all, func() (inventory.UnitBrief, bool, error) { return b.store.UnitBrief(ctx, u) })
}

func withBrief(f workflow.Facts, b *brief.Brief, in brief.Intent) workflow.Facts {
	out := maps.Clone(f)
	if out == nil {
		out = workflow.Facts{}
	}
	maps.Copy(out, brief.Facts(b, in.Schema))
	return out
}
