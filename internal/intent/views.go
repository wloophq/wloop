package intent

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"slices"
	"strings"
	"text/template"
	"time"

	"github.com/wloophq/wloop/internal/domain/duration"
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
	// Empty is what a view without its own empty shows when it lists nothing.
	Empty *template.Template
	// DefaultSort is what clients call a view's own order.
	DefaultSort string
	// Facets are the task facets in their declared order.
	Facets []FacetDef
}

// FacetDef is a task facet: the fact (or the "<record>.<field>" of a
// record kind) it reads, and the mechanics every client filters, groups and
// sorts it by. Merge is "union" or "rows-first", Units "all" or "primary".
type FacetDef struct {
	ID, Fact, Label, Empty, Sort, Match string
	Group, Menu, Strip                  bool
	Rank                                []string
	Values                              map[string]string
	Buckets                             []FacetBucket
	Merge, Units                        string

	from map[string]*sheet
}

// FacetBucket is a bucket of a facet that sorts by time; a Within of 0 takes the rest.
type FacetBucket struct {
	ID, Label string
	Within    time.Duration
}

// Problem is a problem at key of the facet's declaration, in the file of the layer that set it.
func (f FacetDef) Problem(key, format string, args ...any) Problem {
	s := f.from[key]
	if s == nil {
		s = f.from["fact"]
	}
	if s == nil {
		return Problem{File: ViewsFile, Field: "facets." + f.ID + "." + key, Msg: fmt.Sprintf(format, args...)}
	}
	return s.problem("facets."+f.ID+"."+key, format, args...)
}

// ViewDef is a view: Summary, one line on what it lists; what it lists (Over,
// task.OverTasks or task.OverGates); When, which of them, compiled over their
// facts, nil for a view that lists nothing and is a place a client draws
// itself, such as the review; Group the fact it groups by, Sort the facts it
// sorts by, Key the key that jumps to it, HideDisabled whether it leaves out
// what a disabled plugin holds, RemoveClean whether it offers to remove its
// tasks' clean workspaces, and Empty what it shows when it lists nothing.
// WhenText and SortText are its when and sort as the profile writes them.
type ViewDef struct {
	ID, Label, Summary, Over, Group, Key, Empty string
	WhenText, SortText                          string
	Sort                                        []SortKey
	HideDisabled, RemoveClean                   bool
	When                                        workflow.Predicate
	ownEmpty                                    string
	groupAt                                     *sheet
}

func (v ViewDef) groupProblem(format string, args ...any) Problem {
	if v.groupAt == nil {
		return Problem{File: ViewsFile, Field: "views." + v.ID + ".group", Msg: fmt.Sprintf(format, args...)}
	}
	return v.groupAt.problem("views."+v.ID+".group", format, args...)
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
	Empty       *string                `toml:"empty"`
	DefaultSort *string                `toml:"default_sort"`
	Facets      map[string]mFacet      `toml:"facets"`
	Views       map[string]mView       `toml:"views"`
	Review      map[string]mReviewStep `toml:"review"`
	Badge       *mBadge                `toml:"badge"`
}

type mView struct {
	Label        *string `toml:"label"`
	Summary      *string `toml:"summary"`
	Empty        *string `toml:"empty"`
	Over         *string `toml:"over"`
	When         *string `toml:"when"`
	Group        *string `toml:"group"`
	Sort         *string `toml:"sort"`
	Key          *string `toml:"key"`
	HideDisabled *bool   `toml:"hide_disabled"`
	RemoveClean  *bool   `toml:"remove_clean"`
}

type mFacet struct {
	Fact    *string            `toml:"fact"`
	Label   *string            `toml:"label"`
	Group   *bool              `toml:"group"`
	Sort    *string            `toml:"sort"`
	Rank    []string           `toml:"rank"`
	Values  map[string]string  `toml:"values"`
	Empty   *string            `toml:"empty"`
	Match   *string            `toml:"match"`
	Menu    *bool              `toml:"menu"`
	Strip   *bool              `toml:"strip"`
	Buckets map[string]mBucket `toml:"buckets"`
	Merge   *string            `toml:"merge"`
	Units   *string            `toml:"units"`
}

type mBucket struct {
	Within *string `toml:"within"`
	Label  *string `toml:"label"`
}

type mReviewStep struct {
	Label *string `toml:"label"`
}

type mBadge struct {
	Views []string `toml:"views"`
}

// BundledViews is the views of the bundled profile alone.
func BundledViews(bundled fs.FS) (ViewsDef, Problems) {
	out, tasks, problems := loadViews(nil, bundled, nil)
	records, recordProblems := loadRecords(nil, bundled, &tasks)
	return out, slices.Concat(problems, recordProblems, out.checkFacets(tasks, records))
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
	if m.Empty != nil {
		if t, err := template.New("empty").Option("missingkey=error").Parse(*m.Empty); err != nil {
			s.add("empty", "%s", err)
		} else if err := t.Execute(io.Discard, map[string]any{"label": ""}); err != nil {
			s.add("empty", "%s", err)
		} else {
			l.def.Empty = t
		}
	}
	text(s, "default_sort", m.DefaultSort, &l.def.DefaultSort)
	l.mergeFacets(s, m.Facets)
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

func (l *viewsLoad) mergeFacets(s *sheet, facets map[string]mFacet) {
	for _, id := range declared(s, "facets", facets) {
		path, m := "facets."+id, facets[id]
		i := slices.IndexFunc(l.def.Facets, func(f FacetDef) bool { return f.ID == id })
		f := FacetDef{ID: id, Menu: true, Merge: "union", Units: "all", from: map[string]*sheet{}}
		if i >= 0 {
			f = l.def.Facets[i]
			f.from = maps.Clone(f.from)
		}
		n := len(s.problems)
		set := func(key string) { f.from[key] = s }
		switch {
		case m.Fact != nil:
			text(s, path+".fact", m.Fact, &f.Fact)
			set("fact")
		case i < 0:
			s.add(path+".fact", "is required")
		}
		switch {
		case m.Label != nil:
			text(s, path+".label", m.Label, &f.Label)
		case i < 0:
			s.add(path+".label", "is required")
		}
		if m.Empty != nil {
			f.Empty = strings.TrimSpace(*m.Empty)
		}
		for _, o := range []struct {
			key string
			v   *string
			dst *string
		}{{"sort", m.Sort, &f.Sort}, {"match", m.Match, &f.Match}, {"merge", m.Merge, &f.Merge}, {"units", m.Units, &f.Units}} {
			if o.v != nil {
				n := len(s.problems)
				s.oneOf(path+"."+o.key, *o.v, builtin.ValuesOf(builtin.SurfaceViews, "facets.*."+o.key))
				if len(s.problems) == n {
					*o.dst = *o.v
					set(o.key)
				}
			}
		}
		if m.Group != nil {
			f.Group = *m.Group
			set("group")
		}
		if m.Menu != nil {
			f.Menu = *m.Menu
		}
		if m.Strip != nil {
			f.Strip = *m.Strip
		}
		if m.Rank != nil {
			f.Rank = slices.Clone(m.Rank)
		}
		if m.Values != nil {
			f.Values = maps.Clone(m.Values)
		}
		if m.Buckets != nil {
			buckets, bad := []FacetBucket(nil), false
			for _, b := range declared(s, path+".buckets", m.Buckets) {
				bucket := FacetBucket{ID: b, Label: b}
				mb := m.Buckets[b]
				text(s, path+".buckets."+b+".label", mb.Label, &bucket.Label)
				if mb.Within != nil {
					d, err := duration.Parse(*mb.Within)
					if err != nil {
						s.add(path+".buckets."+b+".within", "%s", err)
						bad = true
					}
					bucket.Within = d
				}
				buckets = append(buckets, bucket)
			}
			if !bad {
				f.Buckets = buckets
				set("buckets")
			}
		}
		f.from["id"] = s
		switch {
		case len(s.problems) > n && i < 0:
		case i < 0:
			l.def.Facets = append(l.def.Facets, f)
		default:
			l.def.Facets[i] = f
		}
	}
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
		switch {
		case m.Summary != nil:
			text(s, path+".summary", m.Summary, &v.Summary)
		case i < 0:
			s.add(path+".summary", "is required")
		}
		if m.Empty != nil {
			v.ownEmpty = strings.TrimSpace(*m.Empty)
		}
		if m.Over != nil {
			s.oneOf(path+".over", *m.Over, builtin.ValuesOf(builtin.SurfaceViews, "views.*.over"))
			v.Over = *m.Over
		}
		if m.When != nil {
			delete(l.refs, path+".when")
			v.WhenText = strings.TrimSpace(*m.When)
			if v.WhenText != "" {
				l.refs[path+".when"] = ref{s, path + ".when", *m.When}
			}
		}
		if m.Group != nil {
			v.Group, v.groupAt = strings.TrimSpace(*m.Group), s
		}
		if m.Sort != nil {
			v.Sort, v.SortText = sortKeys(*m.Sort), strings.TrimSpace(*m.Sort)
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
		facts := env.Names()
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
			out = append(out, v.groupProblem("%s names no fact of a view over %s%s", v.Group, v.Over, expr.DidYouMean(v.Group, facts)))
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
		v.Empty = v.ownEmpty
		if v.Empty == "" && l.def.Empty != nil {
			var b strings.Builder
			if err := l.def.Empty.Execute(&b, map[string]any{"label": v.Label}); err == nil {
				v.Empty = b.String()
			}
		}
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

// checkFacets leaves out the facets that read no fact of a task or field of a
// record kind, or sort by time what is no instant, and clears the group of
// a view that no remaining facet reads.
func (d *ViewsDef) checkFacets(tasks TasksDef, records RecordsDef) Problems {
	var out Problems
	env := ViewEnv(task.OverTasks, tasks)
	fields := recordFields(records)
	facts := env.Names()
	names := append(slices.Clone(facts), slices.Sorted(maps.Keys(fields))...)
	d.Facets = slices.DeleteFunc(d.Facets, func(f FacetDef) bool {
		typ, ok := env[f.Fact]
		ok = ok && slices.Contains(facts, f.Fact)
		_, isField := fields[f.Fact]
		switch {
		case !ok && !isField:
			out = append(out, f.Problem("fact", "%s names no fact of a task nor field of a record kind%s", f.Fact, expr.DidYouMean(f.Fact, names)))
		case f.Sort == "time" && (!ok || typ != expr.Time):
			out = append(out, f.Problem("sort", "time sorts instants, and %s is not one", f.Fact))
		default:
			return false
		}
		return true
	})
	for i := range d.Views {
		v := &d.Views[i]
		if v.Over != task.OverTasks || v.Group == "" || slices.ContainsFunc(d.Facets, func(f FacetDef) bool { return f.Fact == v.Group }) {
			continue
		}
		out = append(out, v.groupProblem("%s is read by no facet of %s", v.Group, ViewsFile))
		v.Group = ""
	}
	return out
}

// recordFields are the "<kind>.<field>" names of the record kinds' schemas.
func recordFields(records RecordsDef) map[string]bool {
	out := map[string]bool{}
	for _, k := range records.Kinds {
		for _, p := range k.Schema.Properties() {
			out[k.Name+"."+p.Name] = true
		}
	}
	return out
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
