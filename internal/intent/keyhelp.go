package intent

import (
	"errors"
	"io/fs"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
	"github.com/wloophq/wloop/internal/keys"
	"github.com/wloophq/wloop/internal/profile"
)

// KeysFile is the profile file that words the core keys' help.
const KeysFile = "keys.toml"

// KeyTexts are the help lines of the core keys, by context, then action;
// a numbered action such as answer:3 reads answer:{n}.
type KeyTexts map[string]map[string]string

var numbered = regexp.MustCompile(`:(\d+)$`)

// Help is the help line of action in context, "" when the profile has none.
func (k KeyTexts) Help(context, action string) string {
	if h, ok := k[context][action]; ok {
		return h
	}
	m := numbered.FindStringSubmatch(action)
	if m == nil {
		return ""
	}
	return strings.ReplaceAll(k[context][numbered.ReplaceAllString(action, ":{n}")], "{n}", m[1])
}

// KeyActions are the actions keys.toml words, by context: one per core
// action, a numbered one as <name>:{n}.
func KeyActions() map[string][]string {
	out := map[string][]string{}
	for _, b := range keys.Core {
		c, a := string(b.Context), numbered.ReplaceAllString(b.Action, ":{n}")
		if !slices.Contains(out[c], a) {
			out[c] = append(out[c], a)
		}
	}
	return out
}

type mKeys struct {
	Help map[string]map[string]string `toml:"help"`
}

// BundledKeys is the key help of the bundled profile alone.
func BundledKeys(bundled fs.FS) (KeyTexts, Problems) { return loadKeys(nil, bundled) }

// loadKeys reads keys.toml from bundled and the user's layers: a higher
// layer's line replaces a lower one's, and an empty line or one naming no
// action keeps the lower one. A bundled FS without the file words nothing.
func loadKeys(layers []profile.Layer, bundled fs.FS) (KeyTexts, Problems) {
	out := KeyTexts{}
	if bundled == nil {
		return out, nil
	}
	src, err := fs.ReadFile(bundled, KeysFile)
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, Problems{{File: KeysFile, Msg: err.Error()}}
	}
	actions := KeyActions()
	problems := mergeKeys(out, KeysFile, src, actions)
	for _, l := range layers {
		if l.Bundled() {
			continue
		}
		r, err := profile.ReadFile(l, KeysFile)
		file, src := r.Path, r.Body
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			problems = append(problems, Problem{File: file, Msg: err.Error()})
			continue
		}
		problems = append(problems, mergeKeys(out, file, src, actions)...)
	}
	for _, c := range slices.Sorted(maps.Keys(actions)) {
		for _, a := range actions[c] {
			if out[c][a] == "" {
				problems = append(problems, Problem{File: KeysFile, Field: "help." + c + "." + a, Msg: "is missing: every core key has its help"})
			}
		}
	}
	return out, problems
}

func mergeKeys(out KeyTexts, file string, src []byte, actions map[string][]string) Problems {
	m, problems, _, complete := decode[mKeys](file, src)
	if !complete {
		return problems
	}
	s := &sheet{file: file, at: positions(src), problems: problems}
	contexts := slices.Sorted(maps.Keys(actions))
	for _, c := range slices.Sorted(maps.Keys(m.Help)) {
		if _, ok := actions[c]; !ok {
			s.add("help."+c, "names no key context%s", expr.DidYouMean(c, contexts))
			continue
		}
		if out[c] == nil {
			out[c] = map[string]string{}
		}
		for _, a := range slices.Sorted(maps.Keys(m.Help[c])) {
			path := "help." + c + "." + a
			switch v := strings.TrimSpace(m.Help[c][a]); {
			case !slices.Contains(actions[c], a):
				s.add(path, "names no core key action of %s%s", c, expr.DidYouMean(a, actions[c]))
			case v == "":
				s.add(path, "is required")
			default:
				out[c][a] = v
			}
		}
	}
	s.addGrammar(grammar(builtin.SurfaceKeys, src))
	return s.problems
}
