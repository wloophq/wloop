package cli

import (
	"context"
	"slices"
	"time"

	factentry "github.com/wloophq/wloop/internal/adapter/facts/entry"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/automation"
	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/source"
)

// wireSubjects wires the entry subject's facts provider over the store, and
// a provider that lists nothing for every plugin built without workflow
// facts, a disabled one included, so a workflow on its subject plans
// nothing instead of failing.
func (e *Env) wireSubjects(w *serveWiring) {
	if w.Engine.Subjects == nil {
		w.Engine.Subjects = map[unit.ProviderID]automation.Units{}
	}
	if w.DB != nil {
		w.Engine.Subjects[gtd.Codec{}.Provider()] = e.entryFacts(w.DB)
	}
	for _, p := range e.sourcePlugins() {
		if !slices.ContainsFunc(w.Modules, func(m source.Module) bool { return m.ID == p.ID && m.Facts != nil }) {
			w.Engine.Subjects[p.ID] = noUnits{}
		}
	}
}

// entryFacts is the entry subject's facts provider, reading the config the
// daemon holds at each read.
func (e *Env) entryFacts(db *sqlite.DB) *factentry.Provider {
	enabled := func(p unit.ProviderID) bool { return e.currentConfig().PluginEnabled(string(p)) }
	return factentry.New(factentry.Deps{
		Entries: db.Entries(), Items: db.Items(), Drafts: db.Drafts(),
		Cards:   coregtd.CardFacts{Cards: db.Cards(), Enabled: enabled},
		Enabled: enabled,
		Settings: func() factentry.Settings {
			cfg := e.currentConfig()
			return factentry.Settings{StaleDays: cfg.GTD.StaleWaitingDays, RenudgeAfter: time.Duration(cfg.Proposals.RenudgeAfter)}
		},
		Clock: clockFunc(func() time.Time { return e.Now() }),
	})
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
