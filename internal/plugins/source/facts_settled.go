package source

import (
	"context"
	"maps"
	"slices"
	"time"

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
	at, err := f.settledAt(ctx, wf, u)
	if err != nil {
		return nil, err
	}
	out := maps.Clone(facts)
	out[name] = at
	return out, nil
}

// settledAt is draft.SettledAt of the drafts wf proposed on u; in a pass, that of every unit is read once.
func (f SettledFacts) settledAt(ctx context.Context, wf string, u unit.Unit) (time.Time, error) {
	var all func() (map[unit.Unit]time.Time, error)
	if r, ok := f.Drafts.(outbox.SettledReader); ok {
		all = func() (map[unit.Unit]time.Time, error) { return r.SettledAt(ctx, wf) }
	}
	at, _, err := coretasks.Lookup(ctx, "source.settled."+f.Source.Slot+"."+wf, u, all, func() (time.Time, bool, error) {
		ds, err := f.Drafts.List(ctx, outbox.Filter{Unit: &u, Workflow: wf})
		return draft.SettledAt(ds, wf, u), true, err
	})
	return at, err
}
