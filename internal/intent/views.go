package intent

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
	"github.com/wloophq/wloop/internal/keys"
	"github.com/wloophq/wloop/internal/profile"
)

// ViewsFile is the profile file that declares the views, the review and the badge.
const ViewsFile = "views.toml"

// ViewsDef is views.toml over every layer. Views and Review keep their
// declared order, as TasksDef's states do: the bundled entries first, then
// each user layer's new ones in its own order.
type ViewsDef struct {
	Views []ViewDef
	// Review are the review's steps in order, each a view.
	Review []ReviewStepDef
	// Badge are the views whose counts the badge adds up.
	Badge []string
}

// ViewDef is a view: what it lists (Over, task.OverTasks or task.OverGates),
// When, which of them, compiled over their facts, nil for a view that lists
// nothing and is a place a client draws itself, such as the review; Group the
// fact it groups by, Sort the facts it sorts by, Key the key that jumps to it,
// HideDisabled whether it leaves out what a disabled plugin holds, and
// RemoveClean whether it offers to remove its tasks' clean workspaces.
type ViewDef struct {
	ID, Label, Over, Group, Key string
	Sort                        []SortKey
	HideDisabled, RemoveClean   bool
	When                        workflow.Predicate
}

// SortKey is a fact a view sorts by, descending when Desc.
type SortKey struct {
	Fact string
	Desc bool
}

// ReviewStepDef is a step of the review: the view it walks and what it asks of the user.
type ReviewStepDef struct {
	View, Label string
}

// View is the view id; false when the profile declares none.
func (d ViewsDef) View(id string) (ViewDef, bool) {
	i := slices.IndexFunc(d.Views, func(v ViewDef) bool { return v.ID == id })
	if i < 0 {
		return ViewDef{}, false
	}
	return d.Views[i], true
}

// IDs are the views' ids in order.
func (d ViewsDef) IDs() []string {
	out := make([]string, len(d.Views))
	for i, v := range d.Views {
		out[i] = v.ID
	}
	return out
}

// Badged says whether the badge counts view id.
func (d ViewsDef) Badged(id string) bool { return slices.Contains(d.Badge, id) }

type mViews struct {
	Views  map[string]mView       `toml:"views"`
	Review map[string]mReviewStep `toml:"review"`
	Badge  *mBadge                `toml:"badge"`
}

type mView struct {
	Label        *string `toml:"label"`
	Over         *string `toml:"over"`
	When         *string `toml:"when"`
	Group        *string `toml:"group"`
	Sort         *string `toml:"sort"`
	Key          *string `toml:"key"`
	HideDisabled *bool   `toml:"hide_disabled"`
	RemoveClean  *bool   `toml:"remove_clean"`
}

type mReviewStep struct {
	Label *string `toml:"label"`
}

type mBadge struct {
	Views []string `toml:"views"`
}

// BundledViews is the views of the bundled profile alone.
func BundledViews(bundled fs.FS) (ViewsDef, Problems) {
	out, _, problems := loadViews(nil, bundled, nil)
	return out, problems
}

// viewsLoad is views.toml on its way to a ViewsDef over the layers; the
// checks that need every layer's views and tasks.toml's properties wait in
// refs, by path.
type viewsLoad struct {
	def  ViewsDef
	refs map[string]ref
}

// loadViews reads tasks.toml and views.toml from bundled and the user's
// layers: a higher layer's key replaces a lower one's. A bundled FS without
// a file, or nil, declares nothing of it. A user's file the profile no longer
// reads is a problem naming where its words go now. gates checks the gates a
// verb names, nil none.
func loadViews(layers []profile.Layer, bundled fs.FS, gates *GatesDef) (ViewsDef, TasksDef, Problems) {
	problems := renamedFiles(layers)
	if bundled == nil {
		return ViewsDef{}, TasksDef{Verbs: map[string]VerbText{}}, problems
	}
	tasks, taskProblems := loadTasks(layers, bundled, gates)
	problems = append(problems, taskProblems...)
	l := &viewsLoad{refs: map[string]ref{}}
	views, read := readLayered(layers, bundled, ViewsFile, l.merge)
	problems = append(problems, views...)
	if read {
		problems = append(problems, l.check(tasks)...)
	}
	return l.def, tasks, problems
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
		if l.Bundled() {
			continue
		}
		r, err := profile.ReadFile(l, name)
		file, src := r.Path, r.Body
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
		if l.Bundled() {
			continue
		}
		for _, name := range slices.Sorted(maps.Keys(builtin.RenamedFiles)) {
			if r, err := profile.ReadFile(l, name); err == nil {
				problems = append(problems, Problem{File: r.Path, Msg: name + " is no longer read: " + builtin.RenamedFiles[name]})
			}
		}
	}
	return problems
}

// merge lays file's views, review and badge over the definition so far.
func (l *viewsLoad) merge(file string, src []byte) Problems {
	m, problems, _, complete := decode[mViews](file, src)
	if !complete {
		return problems
	}
	s := &sheet{file: file, at: positions(src), problems: problems}
	l.mergeViews(s, m.Views)
	l.mergeReview(s, m.Review)
	if m.Badge != nil && m.Badge.Views != nil {
		maps.DeleteFunc(l.refs, func(at string, _ ref) bool { return strings.HasPrefix(at, "badge.") })
		l.def.Badge = slices.Clone(m.Badge.Views)
		for i, id := range m.Badge.Views {
			path := fmt.Sprintf("badge.views[%d]", i)
			l.refs[path] = ref{s, path, id}
		}
	}
	s.addGrammar(grammar(builtin.SurfaceViews, src))
	return s.problems
}

func (l *viewsLoad) mergeViews(s *sheet, views map[string]mView) {
	for _, id := range declared(s, "views", views) {
		path, m := "views."+id, views[id]
		i := slices.IndexFunc(l.def.Views, func(v ViewDef) bool { return v.ID == id })
		v := ViewDef{ID: id, Over: task.OverTasks}
		if i >= 0 {
			v = l.def.Views[i]
		}
		n := len(s.problems)
		switch {
		case m.Label != nil:
			text(s, path+".label", m.Label, &v.Label)
		case i < 0:
			s.add(path+".label", "is required")
		}
		if m.Over != nil {
			s.oneOf(path+".over", *m.Over, builtin.ValuesOf(builtin.SurfaceViews, "views.*.over"))
			v.Over = *m.Over
		}
		if m.When != nil {
			delete(l.refs, path+".when")
			if strings.TrimSpace(*m.When) != "" {
				l.refs[path+".when"] = ref{s, path + ".when", *m.When}
			}
		}
		if m.Group != nil {
			v.Group = strings.TrimSpace(*m.Group)
		}
		if m.Sort != nil {
			v.Sort = sortKeys(*m.Sort)
		}
		if m.Key != nil {
			v.Key = ""
			if k := strings.TrimSpace(*m.Key); k != "" {
				if err := keys.CheckView(k); err != nil {
					s.add(path+".key", "%s", err)
				} else {
					v.Key, _ = keys.Canonical(k)
				}
			}
		}
		if m.HideDisabled != nil {
			v.HideDisabled = *m.HideDisabled
		}
		if m.RemoveClean != nil {
			v.RemoveClean = *m.RemoveClean
		}
		switch {
		case len(s.problems) > n && i < 0:
		case i < 0:
			l.def.Views = append(l.def.Views, v)
		default:
			l.def.Views[i] = v
		}
	}
}

func (l *viewsLoad) mergeReview(s *sheet, steps map[string]mReviewStep) {
	for _, id := range declared(s, "review", steps) {
		path := "review." + id
		i := slices.IndexFunc(l.def.Review, func(st ReviewStepDef) bool { return st.View == id })
		st := ReviewStepDef{View: id}
		if i >= 0 {
			st = l.def.Review[i]
		}
		switch m := steps[id]; {
		case m.Label != nil:
			text(s, path+".label", m.Label, &st.Label)
		case i < 0:
			s.add(path+".label", "is required")
		}
		l.refs[path] = ref{s, path, id}
		switch {
		case st.Label == "":
		case i < 0:
			l.def.Review = append(l.def.Review, st)
		default:
			l.def.Review[i] = st
		}
	}
}

// sortKeys reads a view's sort: facts, comma-separated, a leading - sorting one descending.
func sortKeys(src string) []SortKey {
	var out []SortKey
	for _, f := range strings.Split(src, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		name, desc := strings.CutPrefix(f, "-")
		out = append(out, SortKey{Fact: strings.TrimSpace(name), Desc: desc})
	}
	return out
}

// ViewEnv is the facts a view over over reads: a task's, its properties
// typed as tasks declares them, or a gate's.
func ViewEnv(over string, tasks TasksDef) expr.Env {
	if over == task.OverGates {
		return gateEnv()
	}
	env := expr.Env{}
	for name, t := range builtin.WorkflowEnv(builtin.TaskSlot) {
		if strings.HasPrefix(name, builtin.TaskSlot+".") {
			env[name] = t
		}
	}
	maps.Copy(env, tasks.Facts())
	return env
}

// check compiles each view's when, and checks that its group and sort name
// its facts, each review step and badge view a view, and that no two views
// share a key.
func (l *viewsLoad) check(tasks TasksDef) Problems {
	var out Problems
	ids := l.def.IDs()
	for i := range l.def.Views {
		v := &l.def.Views[i]
		path := "views." + v.ID
		env := ViewEnv(v.Over, tasks)
		facts := slices.Sorted(maps.Keys(env))
		v.When = nil
		if r, ok := l.refs[path+".when"]; ok {
			pred, err := expr.CompileWorkflow(r.name, env)
			if err != nil {
				msg, snippet := exprMsg(err)
				p := r.s.problem(r.path, "%s", msg)
				p.Snippet = snippet
				out = append(out, p)
			}
			v.When = pred
		}
		if _, ok := env[v.Group]; v.Group != "" && !ok {
			out = append(out, Problem{File: ViewsFile, Field: path + ".group", Msg: fmt.Sprintf("%s names no fact of a view over %s%s", v.Group, v.Over, expr.DidYouMean(v.Group, facts))})
			v.Group = ""
		}
		var keep []SortKey
		for _, k := range v.Sort {
			if _, ok := env[k.Fact]; !ok {
				out = append(out, Problem{File: ViewsFile, Field: path + ".sort", Msg: fmt.Sprintf("%s names no fact of a view over %s%s", k.Fact, v.Over, expr.DidYouMean(k.Fact, facts))})
				continue
			}
			keep = append(keep, k)
		}
		v.Sort = keep
	}
	for _, at := range slices.Sorted(maps.Keys(l.refs)) {
		r := l.refs[at]
		if strings.HasSuffix(at, ".when") || slices.Contains(ids, r.name) {
			continue
		}
		out = append(out, r.s.problem(r.path, "names no view of %s%s", ViewsFile, expr.DidYouMean(r.name, ids)))
	}
	l.def.Review = slices.DeleteFunc(l.def.Review, func(st ReviewStepDef) bool { return !slices.Contains(ids, st.View) })
	l.def.Badge = slices.DeleteFunc(l.def.Badge, func(id string) bool { return !slices.Contains(ids, id) })
	return append(out, l.sharedKeys()...)
}

// sharedKeys are the views whose key another view has too; the view keeps none.
func (l *viewsLoad) sharedKeys() Problems {
	var problems Problems
	byID := slices.Clone(l.def.Views)
	slices.SortFunc(byID, func(a, b ViewDef) int { return strings.Compare(a.ID, b.ID) })
	owner := map[string]string{}
	for _, v := range byID {
		if v.Key != "" {
			owner[v.Key] = v.ID
		}
	}
	for _, v := range byID {
		if v.Key != "" && owner[v.Key] != v.ID {
			problems = append(problems, Problem{File: ViewsFile, Field: "views." + v.ID + ".key", Msg: v.Key + " is the key of views." + owner[v.Key] + " too"})
			l.def.Views[slices.IndexFunc(l.def.Views, func(x ViewDef) bool { return x.ID == v.ID })].Key = ""
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
