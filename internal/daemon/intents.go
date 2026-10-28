package daemon

import (
	"cmp"
	"fmt"
	"slices"
	"sync"
	"unicode/utf8"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/catalog"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/turn"
)

// loaded is the registry as the profile defines it now.
func (s *Server) loaded() *intentload.Loaded {
	if s.deps.Intents != nil {
		if l := s.deps.Intents(); l != nil {
			return l
		}
	}
	l, err := turn.Bundled()
	if err != nil {
		panic(err)
	}
	return l
}

// bundledWords are the bundled profile's words for the verbs, views, Review
// steps and core keys, which a profile that leaves one out falls back to.
var bundledWords = sync.OnceValue(func() coreWords {
	l, err := turn.Bundled()
	if err != nil {
		panic(err)
	}
	return coreWords{l.Keys, l.Lists, l.Tasks}
})

// isIntent tells an intent the registry offers or a user intent that failed
// to load.
func isIntent(l *intentload.Loaded, name string) bool {
	if _, ok := l.Registry.Get(dintent.Name(name)); ok {
		return true
	}
	_, broken := l.Broken[dintent.Name(name)]
	return broken
}

// intentDef is the definition of name, or why it runs no round.
func intentDef(l *intentload.Loaded, name string) (dintent.Def, error) {
	if d, ok := l.Registry.Get(dintent.Name(name)); ok {
		return d, nil
	}
	if p, ok := l.Broken[dintent.Name(name)]; ok {
		return dintent.Def{}, conflict{msg: fmt.Sprintf("the intent %s has problems:\n%s", name, p.Error())}
	}
	return dintent.Def{}, badRequest{fmt.Sprintf("unknown intent %q; use %s", name, l.Registry.Names())}
}

// intakeIntent tells an action an intake takes: an intent whose manifest
// says intake, or run_now.
func intakeIntent(l *intentload.Loaded, name string) bool {
	if name == "run_now" {
		return true
	}
	d, ok := l.Registry.Get(dintent.Name(name))
	return ok && d.Intake
}

// CheckInputs validates a request's inputs against the intent's definition
// and normalizes them to their types; surface is where the request comes
// from, "" for a client that sends no inputs.
func CheckInputs(reg *dintent.Registry, d dintent.Def, in map[string]any, surface, source string, automated bool) (map[string]any, error) {
	if len(in) == 0 {
		return nil, nil
	}
	names := slices.Sorted(func(yield func(string) bool) {
		for k := range in {
			if !yield(k) {
				return
			}
		}
	})
	out := map[string]any{}
	for _, name := range names {
		i := slices.IndexFunc(d.Inputs, func(x dintent.Input) bool { return x.Name == name })
		if i < 0 {
			return nil, badRequest{undeclared(reg, d, name)}
		}
		spec := d.Inputs[i]
		where := cmp.Or(surface, source)
		switch {
		case spec.From != "user":
			return nil, badRequest{fmt.Sprintf("inputs.%s is filled by "+brand.Name+", not by the request", name)}
		case surface == "" || !slices.Contains(spec.Surfaces, surface):
			return nil, badRequest{fmt.Sprintf("%s is not accepted from %s", spec.Label(), cmp.Or(where, "this request"))}
		case spec.ByHandOnly && automated:
			return nil, badRequest{fmt.Sprintf("%s is for rounds asked by hand only", spec.Label())}
		}
		v, err := dintent.Normalize(spec, in[name])
		if err != nil {
			return nil, badRequest{err.Error()}
		}
		if s, ok := v.(string); ok && spec.Max > 0 && utf8.RuneCountInString(s) > spec.Max {
			return nil, badRequest{fmt.Sprintf("%s takes at most %d characters", spec.Label(), spec.Max)}
		}
		out[name] = v
	}
	return out, nil
}

func undeclared(reg *dintent.Registry, d dintent.Def, name string) string {
	var owners []string
	flag := ""
	for _, o := range reg.All() {
		if o.Name == d.Name {
			continue
		}
		if i := slices.IndexFunc(o.Inputs, func(x dintent.Input) bool { return x.Name == name && x.Flag != "" }); i >= 0 {
			owners, flag = append(owners, string(o.Name)), o.Inputs[i].Flag
		}
	}
	if len(owners) > 0 {
		return flag + " applies to " + dintent.OrList(owners)
	}
	return fmt.Sprintf("%s takes no input %s", d.Name, name)
}

// cutGuidance cuts guidance at the intent's guidance.max runes.
func cutGuidance(d dintent.Def, g string) string {
	if r := []rune(g); d.Guidance.Max > 0 && len(r) > d.Guidance.Max {
		return string(r[:d.Guidance.Max])
	}
	return g
}

// profileWarning is the health warning while a user intent does not load.
func profileWarning(l *intentload.Loaded) string {
	if len(l.Problems) == 0 {
		return ""
	}
	return "profile: " + catalog.FirstProblem(l.Problems)
}
