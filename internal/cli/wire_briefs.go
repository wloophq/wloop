package cli

import "github.com/wloophq/wloop/internal/brief"

// wireBriefs gives each plugin's BriefSource that asks the brief intent's
// fields itself its provider's brief intent, and registers every source
// with the brief queue, which briefs an on-demand provider's units through
// it.
func (e *Env) wireBriefs(w *serveWiring) {
	intents := e.briefIntents(w.Log)
	for _, m := range w.Modules {
		if m.Briefs == nil {
			continue
		}
		if iu, ok := m.Briefs.(brief.IntentUser); ok {
			iu.UseIntent(func() brief.Intent { return intents(m.ID) })
		}
		if q := w.Engine.Briefs; q != nil {
			q.Register(m.ID, m.Briefs)
		}
	}
}
