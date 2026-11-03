package source

import (
	"context"
	"maps"
	"slices"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/outbox"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
)

// SettledFact is the name, after a plugin's slot, of the fact the host
// fills in a workflow's scope: the newest update of a settled draft the
// workflow proposed on the unit.
const SettledFact = "settled_at"

// DraftLister lists the host's drafts.
type DraftLister interface {
	List(ctx context.Context, f outbox.Filter) ([]draft.Draft, error)
}

// SettledFacts are a plugin's UnitFacts with <slot>.settled_at filled in
// each workflow's scope, when the manifest declares that fact.
type SettledFacts struct {
	UnitFacts
	Source *manifest.Source
	Drafts DraftLister
}

var _ automation.Scoper = SettledFacts{}

// Scope sets <slot>.settled_at to the newest settled draft wf proposed on u.
func (f SettledFacts) Scope(ctx context.Context, wf string, u unit.Unit, facts workflow.Facts) (workflow.Facts, error) {
	name := f.Source.Slot + "." + SettledFact
	if f.Drafts == nil || !slices.ContainsFunc(f.Source.Facts, func(m manifest.Fact) bool { return m.Name == name }) {
		return facts, nil
	}
	ds, err := f.settled(ctx, wf, u)
	if err != nil {
		return nil, err
	}
	out := maps.Clone(facts)
	out[name] = draft.SettledAt(ds, wf, u)
	return out, nil
}

// settled are the drafts on u that wf proposed, those settled at least; in a pass, the settled drafts of wf
// are read once for every unit.
func (f SettledFacts) settled(ctx context.Context, wf string, u unit.Unit) ([]draft.Draft, error) {
	if _, ok := coretasks.PassOf(ctx); !ok {
		return f.Drafts.List(ctx, outbox.Filter{Unit: &u, Workflow: wf})
	}
	byUnit, err := coretasks.Once(ctx, "source.settled."+f.Source.Slot+"."+wf, func() (map[unit.Unit][]draft.Draft, error) {
		ds, err := f.Drafts.List(ctx, outbox.Filter{Workflow: wf, States: draft.SettledStates})
		out := map[unit.Unit][]draft.Draft{}
		for _, d := range ds {
			out[d.Unit] = append(out[d.Unit], d)
		}
		return out, err
	})
	return byUnit[u], err
}
