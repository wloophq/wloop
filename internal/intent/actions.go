package intent

import (
	"bytes"
	"maps"
	"slices"
	"strings"
	"text/template"

	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
	"github.com/wloophq/wloop/internal/profile"
)

// ActionsFile is the profile file that words the built-in actions.
const ActionsFile = "actions.toml"

// BuiltinAction is a built-in action's texts, from its actions.toml table.
type BuiltinAction struct {
	Label, Summary string
	Input          struct{ Label, Help string }
	// Texts are note, warning, confirm and why, parsed; a text the table
	// leaves empty is absent.
	Texts map[string]*template.Template
	// Copy are the texts of its copy table, by key (text, html, notice), over the copy facts; nil when it has
	// none.
	Copy map[string]*template.Template
}

// Copies says a gives the person something to copy.
func (a BuiltinAction) Copies() bool { return a.Copy["text"] != nil }

// RenderCopy is copy text name of a over facts, "" when a has none.
func (a BuiltinAction) RenderCopy(name string, facts map[string]any) (string, error) {
	t := a.Copy[name]
	if t == nil {
		return "", nil
	}
	var b bytes.Buffer
	err := t.Execute(&b, facts)
	return b.String(), err
}

// Render is text name of a over facts, "" when a has none.
func (a BuiltinAction) Render(name string, facts map[string]any) (string, error) {
	t := a.Texts[name]
	if t == nil {
		return "", nil
	}
	var b bytes.Buffer
	err := t.Execute(&b, facts)
	return b.String(), err
}

type mBuiltinAction struct {
	Label   *string       `toml:"label"`
	Summary *string       `toml:"summary"`
	Note    *string       `toml:"note"`
	Warning *string       `toml:"warning"`
	Confirm *string       `toml:"confirm"`
	Why     *string       `toml:"why"`
	Input   *mActionInput `toml:"input"`
	Copy    *mActionCopy  `toml:"copy"`
}

type mActionCopy struct {
	Text   *string `toml:"text"`
	HTML   *string `toml:"html"`
	Notice *string `toml:"notice"`
}

type mActionInput struct {
	Label *string `toml:"label"`
	Help  *string `toml:"help"`
}

// loadActions reads actions.toml from bundled and the user's layers, by
// catalog id: a higher layer's key replaces a lower one's, and a user's
// text that fails keeps the lower one. A bundled FS without the file, or
// nil, words no built-in action.
func loadActions(layers []profile.Layer, r layerReader) (map[string]BuiltinAction, Problems) {
	out := map[string]BuiltinAction{}
	problems, read := readLayered(layers, r, ActionsFile, func(file string, src []byte) Problems { return mergeActions(out, file, src, r.funcs()) })
	if !read {
		return out, problems
	}
	for _, id := range builtin.BuiltinActionIDs {
		a, ok := out[id]
		switch {
		case !ok:
			problems = append(problems, Problem{File: ActionsFile, Field: id, Msg: "is missing: every built-in action has a table"})
		case a.Label == "":
			problems = append(problems, Problem{File: ActionsFile, Field: id + ".label", Msg: "is required"})
		case a.Summary == "":
			problems = append(problems, Problem{File: ActionsFile, Field: id + ".summary", Msg: "is required"})
		}
	}
	return out, problems
}

// mergeActions lays the tables of file over out.
func mergeActions(out map[string]BuiltinAction, file string, src []byte, funcs template.FuncMap) Problems {
	m, problems, _, complete := decode[map[string]mBuiltinAction](file, src)
	if !complete {
		return problems
	}
	s := &sheet{file: file, at: positions(src), problems: problems, funcs: funcs}
	for _, id := range slices.Sorted(maps.Keys(m)) {
		if to := builtin.RenamedTo(id); to != "" {
			s.add(id, "names no built-in action: it is %s of %s now", to, GatesFile)
			continue
		}
		if !slices.Contains(builtin.BuiltinActionIDs, id) {
			s.add(id, "names no built-in action%s", expr.DidYouMean(id, builtin.BuiltinActionIDs))
			continue
		}
		out[id] = s.overAction(id, out[id], m[id])
	}
	s.addGrammar(grammar(builtin.SurfaceActions, src))
	return s.problems
}

func (s *sheet) overAction(id string, a BuiltinAction, t mBuiltinAction) BuiltinAction {
	set := func(dst *string, v *string) {
		if v != nil {
			*dst = strings.TrimSpace(*v)
		}
	}
	set(&a.Label, t.Label)
	set(&a.Summary, t.Summary)
	if t.Input != nil {
		set(&a.Input.Label, t.Input.Label)
		set(&a.Input.Help, t.Input.Help)
	}
	a.Texts = maps.Clone(a.Texts)
	if a.Texts == nil {
		a.Texts = map[string]*template.Template{}
	}
	sample := SampleActionFacts()
	if strings.HasPrefix(id, "draft.") {
		sample = SampleDraftActionFacts()
	}
	texts := map[string]*string{"note": t.Note, "warning": t.Warning, "confirm": t.Confirm, "why": t.Why}
	for _, name := range slices.Sorted(maps.Keys(texts)) {
		v := texts[name]
		switch {
		case v == nil:
		case *v == "" && name == "why":
			s.add(id+".why", "may not be empty: a disabled action says why")
		case *v == "":
			delete(a.Texts, name)
		default:
			path := id + "." + name
			tmpl, err := ActionText(s.textFuncs(), path, *v, sample)
			if err != nil {
				_, msg := templateMsg(path, err)
				s.add(path, "%s", msg)
				continue
			}
			a.Texts[name] = tmpl
		}
	}
	s.overCopy(id, &a, t.Copy)
	return a
}

// overCopy lays copy table t over a's copy texts: a text left out keeps the lower layer's, an empty one drops
// it.
func (s *sheet) overCopy(id string, a *BuiltinAction, t *mActionCopy) {
	a.Copy = maps.Clone(a.Copy)
	if t == nil {
		return
	}
	if a.Copy == nil {
		a.Copy = map[string]*template.Template{}
	}
	sample := SampleCopyFacts()
	texts := map[string]*string{"text": t.Text, "html": t.HTML, "notice": t.Notice}
	for _, name := range slices.Sorted(maps.Keys(texts)) {
		v := texts[name]
		path := id + ".copy." + name
		switch {
		case v == nil:
		case *v == "" && name == "text":
			s.add(path, "may not be empty: a copy copies a text")
		case *v == "":
			delete(a.Copy, name)
		default:
			tmpl, err := ActionText(s.textFuncs(), path, *v, sample)
			if err != nil {
				_, msg := templateMsg(path, err)
				s.add(path, "%s", msg)
				continue
			}
			a.Copy[name] = tmpl
		}
	}
	if a.Copy["text"] == nil && len(a.Copy) > 0 {
		s.add(id+".copy.text", "is required")
	}
}
