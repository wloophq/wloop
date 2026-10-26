package cli

import (
	"github.com/wloophq/wloop/internal/brief"
	dunit "github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/profile"
)

// briefIntent is the brief intent of the profile as it loads now: its
// composed schema and its prompts resolved through the layers. A prompt
// or schema that does not resolve falls back to the bundled one, logged.
func (e *Env) briefIntent(logf func(string, ...any)) func() brief.Intent {
	return func() brief.Intent {
		d, ok := e.loadedIntents().Registry.Builder(builtin.BuildsBrief, dunit.GitHub)
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
}
