package intent

import (
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
	"github.com/wloophq/wloop/internal/profile"
)

// GTDFile is the profile file that words the GTD verbs and views.
const GTDFile = "gtd.toml"

// GTDTexts are the words of the GTD verbs, by verb, and the labels of the
// views and of the Review's steps, by id.
type GTDTexts struct {
	Verbs  map[string]VerbText
	Views  map[string]string
	Review map[string]string
}

// VerbText is a verb's words: Input is the question a verb that asks for
// a value puts first.
type VerbText struct{ Label, Summary, Input string }

type mGTD struct {
	Verbs  map[string]mVerbText `toml:"verbs"`
	Views  map[string]mViewText `toml:"views"`
	Review map[string]mViewText `toml:"review"`
}

type mVerbText struct {
	Label   *string `toml:"label"`
	Summary *string `toml:"summary"`
	Input   *string `toml:"input"`
}

type mViewText struct {
	Label *string `toml:"label"`
}

// BundledGTD is the GTD texts of the bundled profile alone.
func BundledGTD(bundled fs.FS) (GTDTexts, Problems) {
	return loadGTD(nil, bundled)
}

// loadGTD reads gtd.toml from bundled and the user's layers: a higher
// layer's key replaces a lower one's, and a user's text that is empty or
// names nothing keeps the lower one. A bundled FS without the file, or nil,
// words nothing.
func loadGTD(layers []profile.Layer, bundled fs.FS) (GTDTexts, Problems) {
	out := GTDTexts{Verbs: map[string]VerbText{}, Views: map[string]string{}, Review: map[string]string{}}
	if bundled == nil {
		return out, nil
	}
	src, err := fs.ReadFile(bundled, GTDFile)
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, Problems{{File: GTDFile, Msg: err.Error()}}
	}
	problems := mergeGTD(out, GTDFile, src)
	for _, l := range layers {
		if l.Dir == "" {
			continue
		}
		file := filepath.Join(l.Dir, GTDFile)
		src, err := os.ReadFile(file)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			problems = append(problems, Problem{File: file, Msg: err.Error()})
			continue
		}
		problems = append(problems, mergeGTD(out, file, src)...)
	}
	for _, v := range builtin.GTDVerbs {
		if _, ok := out.Verbs[v]; !ok {
			problems = append(problems, Problem{File: GTDFile, Field: "verbs." + v, Msg: "is missing: every verb has a table"})
		}
	}
	for _, v := range builtin.GTDVerbInputs {
		if w, ok := out.Verbs[v]; ok && w.Input == "" {
			problems = append(problems, Problem{File: GTDFile, Field: "verbs." + v + ".input", Msg: "is required: the verb asks for a value first"})
		}
	}
	for _, v := range builtin.GTDViews {
		if _, ok := out.Views[v]; !ok {
			problems = append(problems, Problem{File: GTDFile, Field: "views." + v, Msg: "is missing: every view has a table"})
		}
	}
	for _, v := range builtin.GTDReviewSteps {
		if _, ok := out.Review[v]; !ok {
			problems = append(problems, Problem{File: GTDFile, Field: "review." + v, Msg: "is missing: every Review step has a table"})
		}
	}
	return out, problems
}

// mergeGTD lays the tables of file over out.
func mergeGTD(out GTDTexts, file string, src []byte) Problems {
	m, problems, _, complete := decode[mGTD](file, src)
	if !complete {
		return problems
	}
	s := &sheet{file: file, at: positions(src), problems: problems}
	text := func(path string, v *string, dst *string) {
		switch {
		case v == nil:
		case strings.TrimSpace(*v) == "":
			s.add(path, "is required")
		default:
			*dst = strings.TrimSpace(*v)
		}
	}
	for _, id := range slices.Sorted(maps.Keys(m.Verbs)) {
		if !slices.Contains(builtin.GTDVerbs, id) {
			s.add("verbs."+id, "names no verb%s", expr.DidYouMean(id, builtin.GTDVerbs))
			continue
		}
		w, t := out.Verbs[id], m.Verbs[id]
		text("verbs."+id+".label", t.Label, &w.Label)
		text("verbs."+id+".summary", t.Summary, &w.Summary)
		text("verbs."+id+".input", t.Input, &w.Input)
		if w.Label == "" && t.Label == nil {
			s.add("verbs."+id+".label", "is required")
		}
		if w.Summary == "" && t.Summary == nil {
			s.add("verbs."+id+".summary", "is required")
		}
		out.Verbs[id] = w
	}
	labels(s, "views", "view", m.Views, out.Views, builtin.GTDViews)
	labels(s, "review", "Review step", m.Review, out.Review, builtin.GTDReviewSteps)
	s.addGrammar(grammar(builtin.SurfaceGTD, src))
	return s.problems
}

// labels lays the label tables m of section over out, each id one of ids.
func labels(s *sheet, section, noun string, m map[string]mViewText, out map[string]string, ids []string) {
	for _, id := range slices.Sorted(maps.Keys(m)) {
		path := section + "." + id
		if !slices.Contains(ids, id) {
			s.add(path, "names no %s%s", noun, expr.DidYouMean(id, ids))
			continue
		}
		switch v := m[id].Label; {
		case v == nil && out[id] == "":
			s.add(path+".label", "is required")
		case v == nil:
		case strings.TrimSpace(*v) == "":
			s.add(path+".label", "is required")
		default:
			out[id] = strings.TrimSpace(*v)
		}
	}
}
