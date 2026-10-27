// Package intent loads intent manifests from the bundled profile and the
// user's layers into one registry; import it as intentload.
package intent

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"text/template"
	"time"

	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dunit "github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
	"github.com/wloophq/wloop/internal/keys"
	"github.com/wloophq/wloop/internal/profile"
)

type Problem struct {
	File      string
	Line, Col int
	Field     string // "inputs.self_review.persist", "action.enabled"
	Msg       string
	Snippet   string // a predicate problem's expression and caret line; Error leaves it out
}

type Problems []Problem

// Sorted is the problems by file, line, column, field and message.
func (p Problems) Sorted() Problems {
	sorted := slices.Clone(p)
	slices.SortStableFunc(sorted, func(a, b Problem) int {
		if c := strings.Compare(a.File, b.File); c != 0 {
			return c
		}
		if a.Line != b.Line {
			return a.Line - b.Line
		}
		if a.Col != b.Col {
			return a.Col - b.Col
		}
		if c := strings.Compare(a.Field, b.Field); c != 0 {
			return c
		}
		return strings.Compare(a.Msg, b.Msg)
	})
	return sorted
}

// Error is the problems sorted, one "file:line:col: field: msg" per line; a
// problem with no line (a whole directory) reads "dir: msg".
func (p Problems) Error() string {
	sorted := p.Sorted()
	lines := make([]string, len(sorted))
	for i, q := range sorted {
		if q.Line == 0 {
			lines[i] = strings.Join(slices.DeleteFunc([]string{q.File, q.Field, q.Msg}, func(s string) bool { return s == "" }), ": ")
			continue
		}
		if q.Field == "" {
			lines[i] = fmt.Sprintf("%s:%d:%d: %s", q.File, q.Line, q.Col, q.Msg)
			continue
		}
		lines[i] = fmt.Sprintf("%s:%d:%d: %s: %s", q.File, q.Line, q.Col, q.Field, q.Msg)
	}
	return strings.Join(lines, "\n")
}

// TemplateCheck renders a manifest's templates against real samples; internal/turn provides it.
type TemplateCheck func(d dintent.Def, t *template.Template) []Problem

type Loaded struct {
	Registry  *dintent.Registry
	Templates map[dintent.Name]*template.Template // body on a clone of base, missingkey=error
	Programs  map[dintent.Name]Programs
	Problems  Problems                  // user intents that did not load, and overrides read as bundled
	Broken    map[dintent.Name]Problems // user intents that failed, keyed by directory name
	Reverted  map[dintent.Name]Problems // bundled intents read without the user's override, which does not load
	// Warnings are what the loaded intents and workflows declare that loads
	// but is deprecated.
	Warnings Problems

	Workflows       []workflow.Workflow // by order, then name; a user workflow that failed is not here
	BrokenWorkflows map[string]Problems // user workflows that failed, keyed by directory name
	// RevertedWorkflows are bundled workflows read without the user's override, which does not load.
	RevertedWorkflows map[string]Problems

	// Actions are the built-in actions' texts from actions.toml, by catalog id.
	Actions map[string]BuiltinAction
	// Tools are the MCP tools' texts from tools.toml, by tool name.
	Tools map[string]ToolText
	// GTD are the GTD verbs' and views' texts from gtd.toml.
	GTD GTDTexts
	// Keys are the core keys' help from keys.toml.
	Keys KeyTexts
	// EmptyRefusal is what a [[refuse]] case whose why renders empty says,
	// from defaults.toml.
	EmptyRefusal string

	skipped  []dintent.Name // kinds whose blocks need what a reverted override declared
	layers   []profile.Layer
	loadTree func([]profile.Layer, *dintent.Registry, []dintent.Name) (profile.Tree, error)
	treeOnce sync.Once
	tree     profile.Tree
	treeErr  error
}

// Tree is the profile's blocks as of this load, read on the first call and
// kept; an unreadable block is the error every call returns.
func (l *Loaded) Tree() (profile.Tree, error) {
	l.treeOnce.Do(func() {
		load := l.loadTree
		if load == nil {
			load = profile.Load
		}
		l.tree, l.treeErr = load(l.layers, l.Registry, l.BrokenNames())
	})
	return l.tree, l.treeErr
}

type Programs struct {
	Records  *expr.Program
	Headline []*expr.Program // per Headline.New case; nil for the unconditional last case
	Also     []*expr.Program
	// Refuse are the refusals' when, per Refuse of the definition.
	Refuse []*expr.Program
	Action ActionPrograms
}

// unit is one intents/<name>/ directory on its way to a definition.
type unit struct {
	name    dintent.Name
	dir     string // as shown in problems
	source  dintent.Source
	bundled bool
	files   files

	sheet    *sheet
	def      dintent.Def
	defined  bool // decoded completely, so def is worth cross-checking
	tmpl     *template.Template
	programs Programs
	// keyProblems are why a user intent loads without its key.
	keyProblems Problems
	// takenBy is the user intent that takes this bundled intent's role,
	// with its key and its row driving.
	takenBy *unit
	// declaredKey and declaredDrives are what the bundled intent declared
	// before takeRoles handed them over.
	declaredKey    string
	declaredDrives bool
}

func (u *unit) broken() bool { return len(u.sheet.problems) > 0 }

// Load reads intents/ of the bundled FS and of each directory layer; check may be nil.
// The partials under partials/ of each directory layer are parsed over base's;
// one that does not parse, or that a problem names, is left out and the
// bundled partials load instead.
// Layers with an empty Dir are skipped: the bundled intents come from bundled only.
// A user directory named as a bundled intent overrides its files one by one;
// when the result does not load, the bundled intent stays and the problems
// name the user's files.
func Load(layers []profile.Layer, bundled fs.FS, base *template.Template, check TemplateCheck) (*Loaded, Problems) {
	reverted := brokenOverlays{}
	partials := revertedPartials{}
	for {
		set, problems := withPartials(base, layers, partials)
		l, broken := load(layers, bundled, set, check, reverted)
		if partials.breaking(layers, append(problems, l.Problems...)) {
			continue
		}
		if len(broken) == 0 {
			actions, actionProblems := loadActions(layers, bundled)
			tools, toolProblems := loadTools(layers, bundled)
			gtdTexts, gtdProblems := loadGTD(layers, bundled)
			keyTexts, keyProblems := loadKeys(layers, bundled)
			l.Actions, l.Tools, l.GTD, l.Keys = actions, tools, gtdTexts, keyTexts
			l.Problems = slices.Concat(partials.problems(), problems, reverted.problems(), l.Problems, actionProblems, toolProblems, gtdProblems, keyProblems)
			l.Reverted = map[dintent.Name]Problems{}
			for n, r := range reverted {
				l.Reverted[n] = r.problems
			}
			l.skipped = reverted.skipped(l.Registry)
			return l, l.Problems
		}
		for n, r := range broken {
			reverted[n] = r
		}
	}
}

// brokenOverlays are the bundled intents read without their overlay, with
// the problems their overlay caused and what it declared, when it decoded.
type brokenOverlays map[dintent.Name]revert

type revert struct {
	problems Problems
	def      *dintent.Def
}

func (b brokenOverlays) problems() Problems {
	var out Problems
	for _, n := range slices.Sorted(maps.Keys(b)) {
		out = append(out, b[n].problems...)
	}
	return out
}

// skipped is the kinds whose blocks only parse with what a reverted
// override declared: a profile the bundled intents do not name, or dims the
// bundled intent does not list.
func (b brokenOverlays) skipped(r *dintent.Registry) []dintent.Name {
	kinds := profile.Kinds(r)
	var out []dintent.Name
	for n, rv := range b {
		names := []string{string(n)}
		if d := rv.def; d != nil {
			names = append(append(names, d.Profile), profilesOf(d.AlsoProfiles)...)
			if bd, _ := r.Get(n); slices.ContainsFunc(d.Dims, func(dim string) bool { return !slices.Contains(bd.Dims, dim) }) {
				out = append(out, n)
			}
		}
		for _, k := range names {
			if k != "" && !slices.Contains(kinds, profile.Kind(k)) {
				out = append(out, dintent.Name(k))
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// brokenBy is the overlays that break: those that do not load, and those a
// bundled intent without an overlay names as a profile when it breaks, each
// with a problem on the user's files.
func brokenBy(units []*unit) brokenOverlays {
	out := brokenOverlays{}
	overlaid := map[dintent.Name]*unit{}
	for _, u := range units {
		if len(u.files.overlay) == 0 {
			continue
		}
		overlaid[u.name] = u
		if u.broken() {
			out[u.name] = revert{problems: u.files.own(append(slices.Clone(u.sheet.problems), u.keyProblems...), "intent.toml"), def: u.declared()}
		}
	}
	for _, u := range units {
		if !u.bundled || len(u.files.overlay) > 0 || !u.broken() || !u.defined {
			continue
		}
		why := u.sheet.problems.Sorted()[0]
		for _, n := range append([]string{u.def.Profile}, profilesOf(u.def.AlsoProfiles)...) {
			o := overlaid[dintent.Name(n)]
			if o == nil {
				continue
			}
			r := out[o.name]
			r.def = o.declared()
			r.problems = append(r.problems, Problem{File: o.files.overlay[len(o.files.overlay)-1],
				Msg: fmt.Sprintf("this override of %s breaks the bundled %s (%s), so %s loads as bundled", o.name, u.name, Problems{why}.Error(), o.name)})
			out[o.name] = r
		}
	}
	return out
}

func (u *unit) declared() *dintent.Def {
	if !u.defined {
		return nil
	}
	return &u.def
}

func profilesOf(also []dintent.AlsoProfile) []string {
	out := make([]string, len(also))
	for i, a := range also {
		out[i] = a.Profile
	}
	return out
}

// load is one pass of Load; the intents in plain are read without their
// overlay. It returns the overlaid intents that broke.
func load(layers []profile.Layer, bundled fs.FS, base *template.Template, check TemplateCheck, plain brokenOverlays) (*Loaded, brokenOverlays) {
	var units []*unit
	var dirProblems Problems
	bundledUnits := map[dintent.Name]*unit{}
	if bundled != nil {
		entries, _ := fs.ReadDir(bundled, "intents")
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			u := &unit{
				name: dintent.Name(e.Name()), dir: path.Join("intents", e.Name()), bundled: true,
				source: dintent.Source{Layer: "bundled"},
			}
			u.files = files{bundled: bundled, dir: u.dir}
			units = append(units, u)
			bundledUnits[u.name] = u
		}
	}
	seen := map[dintent.Name]string{}
	for _, l := range layers {
		if l.Dir == "" {
			continue
		}
		entries, _ := os.ReadDir(filepath.Join(l.Dir, "intents"))
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := dintent.Name(e.Name())
			dir := filepath.Join(l.Dir, "intents", e.Name())
			if b := bundledUnits[name]; b != nil {
				if _, skip := plain[name]; !skip && len(overrides([]string{dir})) > 0 {
					b.files.overlay = append(b.files.overlay, dir)
					b.source = dintent.Source{Layer: l.Name, Dir: dir, Overrides: overrides(b.files.overlay)}
				}
				continue
			}
			switch {
			case slices.Contains(builtin.ReservedNames, string(name)):
				dirProblems = append(dirProblems, Problem{File: dir, Msg: fmt.Sprintf("%s is reserved: a command, an action or a profile word", name)})
				continue
			case seen[name] != "":
				dirProblems = append(dirProblems, Problem{File: dir, Msg: fmt.Sprintf("%s is defined already in %s", name, seen[name])})
				continue
			}
			seen[name] = dir
			units = append(units, &unit{
				name: name, dir: dir,
				source: dintent.Source{Layer: l.Name, Dir: dir},
				files:  files{dir: dir},
			})
		}
	}
	defaults, defaultProblems := readDefaults(layers, bundled)
	dirProblems = append(dirProblems, defaultProblems...)
	for _, u := range units {
		u.load(base, defaults)
		if u.defined && !u.broken() && check != nil && u.tmpl != nil {
			u.sheet.problems = append(u.sheet.problems, check(u.def, u.tmpl)...)
		}
	}
	crossCheck(units)
	l := &Loaded{
		Templates:    map[dintent.Name]*template.Template{},
		Programs:     map[dintent.Name]Programs{},
		Broken:       map[dintent.Name]Problems{},
		Problems:     dirProblems,
		EmptyRefusal: defaults.Refusal,
		layers:       layers,
	}
	var defs []dintent.Def
	broken := brokenBy(units)
	for _, u := range units {
		if u.broken() {
			u.sheet.problems = append(u.sheet.problems, u.keyProblems...)
			l.Problems = append(l.Problems, u.sheet.problems...)
			if !u.bundled {
				l.Broken[u.name] = u.sheet.problems
			}
			continue
		}
		l.Problems = append(l.Problems, u.keyProblems...)
		l.Warnings = append(l.Warnings, u.sheet.warnings...)
		defs = append(defs, u.def)
		l.Templates[u.name] = u.tmpl
		l.Programs[u.name] = u.programs
	}
	l.Registry = dintent.NewRegistry(defs)
	var wfProblems, wfWarnings Problems
	l.Workflows, l.BrokenWorkflows, l.RevertedWorkflows, wfProblems, wfWarnings = loadWorkflows(layers, bundled, l.turnProblem, l.briefExtras)
	l.Problems = append(l.Problems, wfProblems...)
	l.Warnings = append(l.Warnings, wfWarnings...)
	return l, broken
}

// briefExtras are the brief.extra.* facts of p's brief intent.
func (l *Loaded) briefExtras(p dunit.ProviderID) expr.Env {
	d, _ := l.Registry.Builder(builtin.BuildsBrief, p)
	return builtin.ExtraFacts(d.Extra)
}

func (l *Loaded) turnProblem(name, runner string) (arg, msg string) {
	if _, ok := l.Broken[dintent.Name(name)]; ok {
		return "intent", fmt.Sprintf("the intent %q does not load", name)
	}
	d, ok := l.Registry.Get(dintent.Name(name))
	switch {
	case !ok:
		return "intent", fmt.Sprintf("unknown intent %q; the intents are %s", name, l.Registry.AllNames())
	case d.Builds != "":
		return "intent", fmt.Sprintf("the intent %q builds a %s and is never a turn", name, d.Builds)
	case runner != "" && !d.Runs(runner):
		runners := d.Runners
		if runners == nil {
			runners = []string{"session"}
		}
		return "runner", fmt.Sprintf("the intent %q does not run as %s; its runners are %s", name, runner, strings.Join(runners, ", "))
	}
	return "", ""
}

// BrokenNames is the kinds whose blocks are skipped, sorted: the user
// intents that did not load, and the kinds whose blocks need what a reverted
// override declared.
func (l *Loaded) BrokenNames() []dintent.Name {
	out := append(slices.Collect(maps.Keys(l.Broken)), l.skipped...)
	slices.Sort(out)
	return slices.Compact(out)
}

// Rules is intent's record rules; for a broken user intent the error carries its first problem.
func (l *Loaded) Rules(intent string) (dintent.RecordRules, error) {
	if p := l.Broken[dintent.Name(intent)]; len(p) > 0 {
		return dintent.RecordRules{}, fmt.Errorf("the intent %q does not load: %s", intent, Problems{p.Sorted()[0]}.Error())
	}
	return l.Registry.Rules(intent)
}

func (u *unit) load(base *template.Template, defaults manifestDefaults) {
	file := u.files.path("intent.toml")
	s := &sheet{file: file, defaults: defaults}
	u.sheet = s
	if !nameRe.MatchString(string(u.name)) {
		s.problems = append(s.problems, Problem{File: u.dir, Msg: "an intent name is 2 to 24 lowercase letters, digits and -, starting with a letter"})
	}
	const missing = "missing: every intent directory holds intent.toml and instructions.md.tmpl"
	src, err := u.files.read("intent.toml")
	if err != nil {
		s.problems = append(s.problems, readProblem(file, err, missing))
		return
	}
	tmplFile := u.files.path("instructions.md.tmpl")
	body, bodyErr := u.files.read("instructions.md.tmpl")
	m, problems, skip, complete := decode[manifest](file, src)
	if bodyErr != nil && (m.Builds == "" || !errors.Is(bodyErr, fs.ErrNotExist)) {
		s.problems = append(s.problems, readProblem(tmplFile, bodyErr, missing))
	}
	if !complete {
		s.problems = append(s.problems, problems...)
		return
	}
	defer func() {
		s.problems = slices.DeleteFunc(s.problems, func(p Problem) bool { return skip != "" && unindexed(p.Field) == unindexed(skip) })
		s.problems = append(s.problems, problems...)
		s.addGrammar(grammar(builtin.SurfaceIntent, src))
	}()
	s.at = positions(src)
	d := s.define(u.name, m)
	d.Source = u.source
	d.Bundled = u.bundled
	u.loadExtra(s, &d)
	s.validate(d)
	u.programs = s.compile(d)
	u.programs.Action.ActionTexts = s.parseActionTexts(d.Action)
	if body != nil {
		u.tmpl = s.parseMain(d, base, tmplFile, string(body))
	}
	u.def, u.defined = d, true
}

// crossCheck applies the rules that span manifests: profile references, keys and flags.
// Bundled intents claim first, then user intents, then overrides of bundled
// ones: in a clash, a user intent yields to a bundled one, and an override
// yields to both. A broken intent is checked after every clean one and claims
// no key, flag or workspace.
func crossCheck(units []*unit) {
	rank := func(u *unit) int {
		switch {
		case len(u.files.overlay) > 0:
			return 2
		case u.bundled:
			return 0
		}
		return 1
	}
	ordered := slices.Clone(units)
	slices.SortStableFunc(ordered, func(a, b *unit) int {
		if c := rank(a) - rank(b); c != 0 {
			return c
		}
		return strings.Compare(string(a.name), string(b.name))
	})
	defs := map[dintent.Name]dintent.Def{}
	for _, u := range ordered {
		if u.defined {
			defs[u.name] = u.def
		}
	}
	for _, u := range ordered {
		if u.defined {
			checkProfiles(u, defs)
		}
	}
	refusesBrokenProfiles(ordered)
	takeRoles(ordered)
	c := newClaims()
	var broken []*unit
	for _, u := range ordered {
		switch {
		case !u.defined:
		case u.broken():
			broken = append(broken, u)
		default:
			c.check(u, true)
		}
	}
	for _, u := range broken {
		c.check(u, false)
	}
	for _, u := range ordered {
		if t := u.takenBy; t != nil && t.broken() {
			u.def.Key, u.def.DrivesRow = u.declaredKey, u.declaredDrives
		}
	}
}

// takeRoles hands each bundled intent's role to the clean user intent that
// holds the same role on one of its providers: the user's holder takes its
// key and its row driving; the bundled one keeps loading without them.
func takeRoles(ordered []*unit) {
	for _, u := range ordered {
		if !u.defined || u.broken() || u.bundled || u.def.Role == "" {
			continue
		}
		for _, b := range ordered {
			if b.defined && b.bundled && b.def.Role == u.def.Role && slices.ContainsFunc(providersOf(u.def), func(p dunit.ProviderID) bool {
				return slices.Contains(providersOf(b.def), p)
			}) {
				b.takenBy = u
			}
		}
	}
	for _, b := range ordered {
		if b.takenBy != nil {
			b.declaredKey, b.declaredDrives = b.def.Key, b.def.DrivesRow
			b.def.Key, b.def.DrivesRow = "", false
		}
	}
}

// providersOf is the providers whose units d's turns run on.
func providersOf(d dintent.Def) []dunit.ProviderID {
	if d.Providers != nil {
		return d.Providers
	}
	return []dunit.ProviderID{d.ProviderOrDefault()}
}

func checkProfiles(u *unit, defs map[dintent.Name]dintent.Def) {
	s, d := u.sheet, u.def
	if d.Profile != "" {
		if _, ok := defs[dintent.Name(d.Profile)]; !ok {
			s.add("profile", "%s is not an intent; name an intent with judgment blocks, or none", d.Profile)
		}
	}
	for i, a := range d.AlsoProfiles {
		if o, ok := defs[dintent.Name(a.Profile)]; a.Profile != "" && (!ok || o.Profile == "") {
			s.add(fmt.Sprintf("also_profile[%d].profile", i), "%s is not an intent with judgment blocks", a.Profile)
		}
		if a.Define != "" && u.tmpl != nil && u.tmpl.Lookup(a.Define) == nil {
			s.add(fmt.Sprintf("also_profile[%d].define", i), "no template defines %s", a.Define)
		}
	}
}

// refusesBrokenProfiles breaks, until none is left, every clean intent whose
// profile or also_profile names a broken one, since that one's blocks are skipped.
func refusesBrokenProfiles(ordered []*unit) {
	byName := map[dintent.Name]*unit{}
	for _, u := range ordered {
		byName[u.name] = u
	}
	broken := func(name string) bool {
		o, ok := byName[dintent.Name(name)]
		return ok && o.defined && o.broken()
	}
	for changed := true; changed; {
		changed = false
		for _, u := range ordered {
			if !u.defined || u.broken() {
				continue
			}
			if broken(u.def.Profile) {
				u.sheet.add("profile", "%s does not load; fix its manifest, or name another intent", u.def.Profile)
			}
			for i, a := range u.def.AlsoProfiles {
				if broken(a.Profile) {
					u.sheet.add(fmt.Sprintf("also_profile[%d].profile", i), "%s does not load; fix its manifest, or name another intent", a.Profile)
				}
			}
			changed = changed || u.broken()
		}
	}
}

type meaning struct {
	intent dintent.Name
	input  string
	typ    string
}

// claims are the keys, flags and row-driven workspaces the clean intents hold;
// a key's claim names its holder as a key problem reads it.
type claims struct {
	keys    map[string]string
	flags   map[string]meaning
	drivers map[string]dintent.Name
	roles   map[roleClaim]dintent.Name
}

// roleClaim is a role on one provider in one layer: a user intent takes a
// bundled intent's role, two in one layer clash.
type roleClaim struct {
	bundled  bool
	role     string
	provider dunit.ProviderID
}

func newClaims() *claims {
	ids := map[string]string{}
	for id, k := range builtin.ActionKeys {
		if prev, ok := ids[k]; !ok || id < prev {
			ids[k] = id
		}
	}
	keys := map[string]string{}
	for k, id := range ids {
		keys[k] = "the built-in action " + id
	}
	return &claims{keys: keys, flags: map[string]meaning{}, drivers: map[string]dintent.Name{}, roles: map[roleClaim]dintent.Name{}}
}

// key is d's key in canonical form, or why it may not take it on one of the
// surfaces its action reaches.
func (c *claims) key(d dintent.Def) (string, error) {
	var surfaces []keys.Surface
	if d.Action != nil {
		surfaces = keys.SurfacesOf(d.Action.Surfaces)
	}
	if len(surfaces) == 0 {
		surfaces = []keys.Surface{keys.Web, keys.TUI, keys.Menubar}
	}
	for _, s := range surfaces {
		if err := keys.Check(s, d.Key, string(d.Name), c.keys, nil); err != nil {
			return "", err
		}
	}
	return keys.Canonical(d.Key)
}

// check adds u's clashes with the claims held; u takes its own claims only
// when claim is set and it has no problem after the check. A user intent
// whose key is refused keeps loading, without the key.
func (c *claims) check(u *unit, claim bool) {
	s, d := u.sheet, u.def
	var take []func()
	if d.Key != "" {
		switch k, err := c.key(d); {
		case err == nil:
			u.def.Key = k
			take = append(take, func() { c.keys[k] = string(d.Name) })
		case u.bundled:
			s.add("shortcut", "%v", err)
		default:
			u.def.Key = ""
			u.keyProblems = append(u.keyProblems, s.problem("shortcut", "%v; the intent loads without a shortcut (palette only)", err))
		}
	}
	if d.DrivesRow {
		if owner, taken := c.drivers[d.Workspace]; taken {
			s.add("workspace", "%s drives the rows of %s workspaces already: at most one intent per workspace, and %s drives them by its role %s", owner, d.Workspace, d.Name, d.Role)
		} else {
			take = append(take, func() { c.drivers[d.Workspace] = d.Name })
		}
	}
	if d.Role != "" || d.Builds != "" {
		for _, p := range providersOf(d) {
			rc := roleClaim{u.bundled, d.Role, p}
			if d.Builds != "" {
				rc.role = "builds " + d.Builds
			}
			if owner, taken := c.roles[rc]; taken && owner != d.Name && d.Builds != "" {
				s.add("builds", "%s builds the %s of %s units already; one builder per provider", owner, d.Builds, p)
				continue
			} else if taken && owner != d.Name {
				s.add("role", "%s holds the role %s on %s units already; one intent per role and provider", owner, d.Role, p)
				continue
			}
			take = append(take, func() { c.roles[rc] = d.Name })
		}
	}
	own := map[string]meaning{}
	for _, in := range d.Inputs {
		if in.Flag == "" {
			continue
		}
		want := meaning{d.Name, in.Name, in.Type}
		got, ok := c.flags[in.Flag]
		if !ok {
			got, ok = own[in.Flag]
		}
		if ok && (got.input != want.input || got.typ != want.typ || got.intent == want.intent) {
			s.add("inputs."+in.Name+".flag", "%s means %s (%s) in %s", in.Flag, got.input, got.typ, got.intent)
			continue
		}
		if !ok {
			own[in.Flag] = want
		}
	}
	if claim && !u.broken() {
		for _, f := range take {
			f()
		}
		maps.Copy(c.flags, own)
	}
}

// CacheOptions tune a Cache; the zero value rechecks the stamp on every Get.
type CacheOptions struct {
	// Recheck is how long a stamp is trusted before the layers are walked again.
	Recheck time.Duration
	// Now is the clock; time.Now when nil.
	Now func() time.Time
	// LoadTree reads the profile tree of a Loaded; profile.Load when nil.
	LoadTree func([]profile.Layer, *dintent.Registry, []dintent.Name) (profile.Tree, error)
	// OnReload is called, holding no lock, after Get replaces the loaded registry with a changed one; the first load is not a reload.
	OnReload func()
}

// Cache reloads when the newest mtime under the user layer's intents/ and blocks changes; safe for concurrent use.
type Cache struct {
	layers  func() []profile.Layer
	bundled fs.FS
	base    *template.Template
	check   TemplateCheck
	opts    CacheOptions

	mu      sync.Mutex
	stamp   string
	checked time.Time
	walks   int
	loaded  *Loaded
}

func NewCache(layers func() []profile.Layer, bundled fs.FS, base *template.Template, check TemplateCheck, opts CacheOptions) *Cache {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Cache{layers: layers, bundled: bundled, base: base, check: check, opts: opts}
}

func (c *Cache) Get() *Loaded {
	loaded, reloaded := c.get()
	if reloaded && c.opts.OnReload != nil {
		c.opts.OnReload()
	}
	return loaded
}

func (c *Cache) get() (loaded *Loaded, reloaded bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.opts.Now()
	if c.loaded != nil && now.Sub(c.checked) < c.opts.Recheck {
		return c.loaded, false
	}
	layers := c.layers()
	stamp := stampOf(layers)
	c.walks++
	c.checked = now
	if c.loaded == nil || stamp != c.stamp {
		reloaded = c.loaded != nil
		c.loaded, _ = Load(layers, c.bundled, c.base, c.check)
		c.loaded.loadTree = c.opts.LoadTree
		_, _ = c.loaded.Tree()
		c.stamp = stamp
	}
	return c.loaded, reloaded
}

// stampOf is each layer's directory, file count and newest mtime: a change to
// a manifest, a template or a block moves it.
func stampOf(layers []profile.Layer) string {
	var b strings.Builder
	for _, l := range layers {
		if l.Dir == "" {
			continue
		}
		var n int
		var newest time.Time
		root, err := filepath.EvalSymlinks(l.Dir)
		if err != nil {
			root = l.Dir
		}
		_ = filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if e.IsDir() && e.Name() == ".git" {
				return filepath.SkipDir
			}
			if info, err := e.Info(); err == nil {
				n++
				if info.ModTime().After(newest) {
					newest = info.ModTime()
				}
			}
			return nil
		})
		fmt.Fprintf(&b, "%s\x00%s\x00%d\x00%d\x00", l.Dir, root, n, newest.UnixNano())
	}
	return b.String()
}

var index = regexp.MustCompile(`\[\d+\]`)

// unindexed is a field path without array indexes, as go-toml spells a key.
func unindexed(field string) string { return index.ReplaceAllString(field, "") }
