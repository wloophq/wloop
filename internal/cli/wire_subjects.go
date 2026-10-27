package cli

import (
	"context"
	"slices"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/source"
)

// wireSubjects wires the entry subject's facts provider over the store and
// the entries entry.close closes, and
// a provider that lists nothing for every plugin built without workflow
// facts, a disabled one included, so a workflow on its subject plans
// nothing instead of failing.
func (e *Env) wireSubjects(w *serveWiring) {
	if w.Engine.Subjects == nil {
		w.Engine.Subjects = map[unit.ProviderID]automation.Units{}
	}
	if w.DB != nil {
		w.Engine.Subjects[gtd.Codec{}.Provider()] = e.entryFacts(w.DB, unitsEnded(w.Modules))
	}
	if e.gtd != nil {
		w.Engine.Entries = e.gtd
	}
	for _, p := range e.sourcePlugins() {
		if !slices.ContainsFunc(w.Modules, func(m source.Module) bool { return m.ID == p.ID && m.Facts != nil }) {
			w.Engine.Subjects[p.ID] = noUnits{}
		}
	}
}

// noUnits is the subject of a plugin that is disabled or offers no
// workflow facts.
type noUnits struct{}

func (noUnits) List(context.Context, unit.ProviderID) ([]unit.Unit, error) { return nil, nil }

func (noUnits) Facts(context.Context, unit.Unit) (workflow.Facts, error) {
	return nil, automation.ErrUnitGone
}

var _ automation.Scoper = (*unitSet)(nil)

// Scope is the scoped facts of u's provider; f unchanged when it scopes
// none.
func (s *unitSet) Scope(ctx context.Context, wf string, u unit.Unit, f workflow.Facts) (workflow.Facts, error) {
	if sc, ok := s.providers[u.Provider].(automation.Scoper); ok {
		return sc.Scope(ctx, wf, u, f)
	}
	return f, nil
}
