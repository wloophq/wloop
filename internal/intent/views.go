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
	"github.com/wloophq/wloop/internal/keys"
	"github.com/wloophq/wloop/internal/profile"
)

// ViewsFile is the profile file that words the views and the review.
const ViewsFile = "views.toml"

// ListTexts are the labels of the views and of the review's steps, by id,
// from views.toml.
type ListTexts struct {
	Views  map[string]string
	Review map[string]string
	// Keys are the keys that jump to the views, by view; a view without one has none.
	Keys map[string]string
}

type mViews struct {
	Views  map[string]mViewText   `toml:"views"`
	Review map[string]mReviewText `toml:"review"`
	Badge  *mBadge                `toml:"badge"`
}

type mViewText struct {
	Label        *string `toml:"label"`
	Over         *string `toml:"over"`
	When         *string `toml:"when"`
	Group        *string `toml:"group"`
	Sort         *string `toml:"sort"`
	Key          *string `toml:"key"`
	HideDisabled *bool   `toml:"hide_disabled"`
}

type mReviewText struct {
	Label *string `toml:"label"`
}

type mBadge struct {
	Views []string `toml:"views"`
}

// BundledLists is the views' texts of the bundled profile alone.
func BundledLists(bundled fs.FS) (ListTexts, Problems) {
	out, _, problems := loadLists(nil, bundled, nil)
	return out, problems
}

// loadLists reads tasks.toml and views.toml from bundled and the user's
// layers: a higher layer's key replaces a lower one's, and a user's text
// that is empty or names nothing keeps the lower one. A bundled FS without
// a file, or nil, words nothing of it. A user's file the profile no longer
// reads is a problem naming where its words go now. The verbs' texts are
// the TasksDef's; gates checks the gates a verb names, nil none.
func loadLists(layers []profile.Layer, bundled fs.FS, gates *GatesDef) (ListTexts, TasksDef, Problems) {
	out := ListTexts{Views: map[string]string{}, Review: map[string]string{}, Keys: map[string]string{}}
	problems := renamedFiles(layers)
	if bundled == nil {
		return out, TasksDef{Verbs: map[string]VerbText{}}, problems
	}
	tasks, taskProblems := loadTasks(layers, bundled, gates)
	problems = append(problems, taskProblems...)
	views, read := readLayered(layers, bundled, ViewsFile, func(file string, src []byte) Problems {
		return mergeViews(out, file, src)
	})
	problems = append(problems, views...)
	if read {
		problems = append(problems, missingViews(out)...)
		problems = append(problems, sharedViewKeys(out)...)
	}
	return out, tasks, problems
}

// readLayered calls merge on name in bundled, then in each user layer that
// has it, and says whether bundled has it. A bundled FS without the file
// reads no layer's.
func readLayered(layers []profile.Layer, bundled fs.FS, name string, merge func(file string, src []byte) Problems) (Problems, bool) {
	if bundled == nil {
		return nil, false
	}
	src, err := fs.ReadFile(bundled, name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false
	}
	if err != nil {
		return Problems{{File: name, Msg: err.Error()}}, false
	}
	problems := merge(name, src)
	for _, l := range layers {
		if l.Dir == "" {
			continue
		}
		file := filepath.Join(l.Dir, name)
		src, err := os.ReadFile(file)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			problems = append(problems, Problem{File: file, Msg: err.Error()})
			continue
		}
		problems = append(problems, merge(file, src)...)
	}
	return problems, true
}

// renamedFiles are the problems of the user's files that builtin.RenamedFiles names.
func renamedFiles(layers []profile.Layer) Problems {
	var problems Problems
	for _, l := range layers {
		if l.Dir == "" {
			continue
		}
		for _, name := range slices.Sorted(maps.Keys(builtin.RenamedFiles)) {
			file := filepath.Join(l.Dir, name)
			if _, err := os.Stat(file); err == nil {
				problems = append(problems, Problem{File: file, Msg: name + " is no longer read: " + builtin.RenamedFiles[name]})
			}
		}
	}
	return problems
}

// mergeViews lays the views and the review of file over out.
func mergeViews(out ListTexts, file string, src []byte) Problems {
	m, problems, _, complete := decode[mViews](file, src)
	if !complete {
		return problems
	}
	s := &sheet{file: file, at: positions(src), problems: problems}
	viewLabels := map[string]*string{}
	for id, v := range m.Views {
		viewLabels[id] = v.Label
	}
	stepLabels := map[string]*string{}
	for id, v := range m.Review {
		stepLabels[id] = v.Label
	}
	labels(s, "views", "view", viewLabels, out.Views, builtin.Views)
	for _, id := range slices.Sorted(maps.Keys(m.Views)) {
		if k := m.Views[id].Key; k != nil && strings.TrimSpace(*k) != "" && slices.Contains(builtin.Views, id) {
			if err := keys.CheckView(strings.TrimSpace(*k)); err != nil {
				s.add("views."+id+".key", "%s", err)
				continue
			}
			out.Keys[id], _ = keys.Canonical(strings.TrimSpace(*k))
		}
	}
	labels(s, "review", "review step", stepLabels, out.Review, builtin.ReviewSteps)
	s.addGrammar(grammar(builtin.SurfaceViews, src))
	return s.problems
}

// sharedViewKeys are the views whose key another view has too; the view keeps none.
func sharedViewKeys(out ListTexts) Problems {
	var problems Problems
	owner := map[string]string{}
	for _, id := range slices.Sorted(maps.Keys(out.Keys)) {
		owner[out.Keys[id]] = id
	}
	for _, id := range slices.Sorted(maps.Keys(out.Keys)) {
		if k := out.Keys[id]; owner[k] != id {
			problems = append(problems, Problem{File: ViewsFile, Field: "views." + id + ".key", Msg: k + " is the key of views." + owner[k] + " too"})
			delete(out.Keys, id)
		}
	}
	return problems
}

// missingViews are the views and the review's steps out words none of.
func missingViews(out ListTexts) Problems {
	var problems Problems
	for _, v := range builtin.Views {
		if _, ok := out.Views[v]; !ok {
			problems = append(problems, Problem{File: ViewsFile, Field: "views." + v, Msg: "is missing: every view has a table"})
		}
	}
	for _, v := range builtin.ReviewSteps {
		if _, ok := out.Review[v]; !ok {
			problems = append(problems, Problem{File: ViewsFile, Field: "review." + v, Msg: "is missing: every review step has a table"})
		}
	}
	return problems
}

// text lays v, the text at path, over dst: absent keeps it, blank is a problem.
func text(s *sheet, path string, v *string, dst *string) {
	switch {
	case v == nil:
	case strings.TrimSpace(*v) == "":
		s.add(path, "is required")
	default:
		*dst = strings.TrimSpace(*v)
	}
}

// labels lays the label tables m of section over out, each id one of ids.
func labels(s *sheet, section, noun string, m map[string]*string, out map[string]string, ids []string) {
	for _, id := range slices.Sorted(maps.Keys(m)) {
		path := section + "." + id
		if !slices.Contains(ids, id) {
			s.add(path, "names no %s%s", noun, expr.DidYouMean(id, ids))
			continue
		}
		switch v := m[id]; {
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
