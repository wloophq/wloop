package cli

import (
	"sync"

	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/domain/unit"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/profile"
)

// briefIntents are the brief intents of the profile as it loads now, by
// the provider whose units they brief: each one's composed schema and its
// prompts resolved through the layers. A prompt or schema that does not
// resolve falls back to the bundled one, logged.
//
// The answers are kept while the profile's intents stay loaded as they are.
func (e *Env) briefIntents(logf func(string, ...any)) func(unit.ProviderID) brief.Intent {
	var (
		mu   sync.Mutex
		seen *intentload.Loaded
		last map[unit.ProviderID]brief.Intent
	)
	return func(p unit.ProviderID) brief.Intent {
		l := e.loadedIntents()
		mu.Lock()
		defer mu.Unlock()
		if l != seen {
			seen, last = l, map[unit.ProviderID]brief.Intent{}
		}
		in, ok := last[p]
		if !ok {
			in = e.resolveBriefIntent(l, p, logf)
			last[p] = in
		}
		return in
	}
}

func (e *Env) resolveBriefIntent(l *intentload.Loaded, p unit.ProviderID, logf func(string, ...any)) brief.Intent {
	d, ok := l.Registry.Builder(builtin.BuildsBrief, p)
	if !ok {
		return brief.Intent{}
	}
	var out brief.Intent
	schema, err := brief.ComposeExtra(brief.Schema, d.Extra)
	if err != nil {
		logf("brief: %s's schema: %v; briefs carry no extra fields", d.Name, err)
	} else {
		out.Schema = schema
	}
	layers := e.intentState().profileLayers(e)
	for _, p := range []struct {
		rel string
		dst *string
	}{{d.Prompt, &out.Prompt}, {brief.FieldsPromptFile, &out.FieldsPrompt}} {
		if p.rel == "" {
			continue
		}
		r, err := profile.Resolve(layers, p.rel)
		if err != nil {
			logf("brief: %s: %v; the bundled prompt", p.rel, err)
			continue
		}
		*p.dst = string(r.Body)
	}
	return out
}
