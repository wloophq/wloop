package cli

import (
	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// briefWriter is a built-in module's BriefSource whose job writes its
// units' briefs in process; a source plugin writes them through the
// broker's PutBrief instead.
type briefWriter interface {
	Watch(noted func(u unit.Unit))
}

// briefIntentUser is a built-in module's BriefSource whose job asks the
// brief intent's extra fields itself.
type briefIntentUser interface {
	UseIntent(intent func() brief.Intent)
}

// wireBriefs gives each module's BriefSource that asks the brief intent's
// fields itself its provider's brief intent, and registers every source
// with the brief queue, which hears of each brief a module's job writes.
func (e *Env) wireBriefs(w *serveWiring) {
	intents := e.briefIntents(w.Log)
	q := w.Engine.Briefs
	if e.briefsHeard != nil {
		e.briefsHeard.Store(q)
	}
	for _, m := range w.Modules {
		if m.Briefs == nil {
			continue
		}
		if iu, ok := m.Briefs.(briefIntentUser); ok {
			iu.UseIntent(func() brief.Intent { return intents(m.ID) })
		}
		if q == nil {
			continue
		}
		q.Register(m.ID, m.Briefs)
		if bw, ok := m.Briefs.(briefWriter); ok {
			bw.Watch(q.Noted)
		}
	}
}
