// Package intent loads intent manifests from the bundled profile and the
// user's layers into one registry; import it as intentload.
package intent

import (
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
	Problems  Problems                  // user intents that did not load; the bundled layer never has problems
	Broken    map[dintent.Name]Problems // user intents that failed, keyed by directory name

	Workflows       []workflow.Workflow // by order, then name; a user workflow that failed is not here
	BrokenWorkflows map[string]Problems // user workflows that failed, keyed by directory name

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
	Records     *expr.Program
	Headline    []*expr.Program // per Headline.New case; nil for the unconditional last case
	Also        []*expr.Program
	Visible     *expr.Program
	Enabled     *expr.Program
	Primary     *expr.Program
	ActionTexts map[string]*template.Template // reason_off, confirm, hint over the action fact map
}

// unit is one intents/<name>/ directory on its way to a definition.
type unit struct {
	name    dintent.Name
	dir     string // as shown in problems
	source  dintent.Source
	bundled bool
	read    func(file string) ([]byte, error)

	sheet    *sheet
	def      dintent.Def
	defined  bool // decoded completely, so def is worth cross-checking
	tmpl     *template.Template
	programs Programs
	// keyProblems are why a user intent loads without its key.
	keyProblems Problems
}

func (u *unit) broken() bool { return len(u.sheet.problems) > 0 }

// Load reads intents/ of the bundled FS and of each directory layer; check may be nil.
// Layers with an empty Dir are skipped: the bundled intents come from bundled only.
func Load(layers []profile.Layer, bundled fs.FS, base *template.Template, check TemplateCheck) (*Loaded, Problems) {
	var units []*unit
	var dirProblems Problems
	if bundled != nil {
		entries, _ := fs.ReadDir(bundled, "intents")
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			dir := path.Join("intents", e.Name())
			units = append(units, &unit{
				name: dintent.Name(e.Name()), dir: dir, bundled: true,
				source: dintent.Source{Layer: "bundled"},
				read:   func(f string) ([]byte, error) { return fs.ReadFile(bundled, path.Join(dir, f)) },
			})
		}
	}
	bundledNames := map[dintent.Name]bool{}
	for _, u := range units {
		bundledNames[u.name] = true
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
			switch {
			case bundledNames[name]:
				dirProblems = append(dirProblems, Problem{File: dir, Msg: fmt.Sprintf("%s is bundled; change its judgment with intent=%s.md blocks", name, name)})
				continue
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
				read:   func(f string) ([]byte, error) { return os.ReadFile(filepath.Join(dir, f)) },
			})
		}
	}
	for _, u := range units {
		u.load(base)
		if u.defined && !u.broken() && check != nil {
			u.sheet.problems = append(u.sheet.problems, check(u.def, u.tmpl)...)
		}
	}
	crossCheck(units)
	l := &Loaded{
		Templates: map[dintent.Name]*template.Template{},
		Programs:  map[dintent.Name]Programs{},
		Broken:    map[dintent.Name]Problems{},
		Problems:  dirProblems,
		layers:    layers,
	}
	var defs []dintent.Def
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
		defs = append(defs, u.def)
		l.Templates[u.name] = u.tmpl
		l.Programs[u.name] = u.programs
	}
	l.Registry = dintent.NewRegistry(defs)
	var wfProblems Problems
	l.Workflows, l.BrokenWorkflows, wfProblems = loadWorkflows(layers, bundled, l.intentProblem)
	l.Problems = append(l.Problems, wfProblems...)
	return l, l.Problems
}

func (l *Loaded) intentProblem(name string) string {
	if _, ok := l.Broken[dintent.Name(name)]; ok {
		return fmt.Sprintf("the intent %q does not load", name)
	}
	if _, ok := l.Registry.Get(dintent.Name(name)); !ok {
		return fmt.Sprintf("unknown intent %q; the intents are %s", name, l.Registry.AllNames())
	}
	return ""
}

// BrokenNames is the names of the user intents that did not load, sorted.
func (l *Loaded) BrokenNames() []dintent.Name {
	return slices.Sorted(maps.Keys(l.Broken))
}

// Rules is intent's record rules; for a broken user intent the error carries its first problem.
func (l *Loaded) Rules(intent string) (dintent.RecordRules, error) {
	if p := l.Broken[dintent.Name(intent)]; len(p) > 0 {
		return dintent.RecordRules{}, fmt.Errorf("the intent %q does not load: %s", intent, Problems{p.Sorted()[0]}.Error())
	}
	return l.Registry.Rules(intent)
}

func (u *unit) join(f string) string {
	if u.bundled {
		return path.Join(u.dir, f)
	}
	return filepath.Join(u.dir, f)
}

func (u *unit) load(base *template.Template) {
	file := u.join("intent.toml")
	s := &sheet{file: file}
	u.sheet = s
	if !nameRe.MatchString(string(u.name)) {
		s.problems = append(s.problems, Problem{File: u.dir, Msg: "an intent name is 2 to 24 lowercase letters, digits and -, starting with a letter"})
	}
	src, err := u.read("intent.toml")
	if err != nil {
		s.problems = append(s.problems, Problem{File: file, Msg: "missing: every intent directory holds intent.toml and instructions.md.tmpl"})
		return
	}
	tmplFile := u.join("instructions.md.tmpl")
	body, err := u.read("instructions.md.tmpl")
	if err != nil {
		s.problems = append(s.problems, Problem{File: tmplFile, Msg: "missing: every intent directory holds intent.toml and instructions.md.tmpl"})
	}
	m, problems, skip, complete := decode[manifest](file, src)
	if !complete {
		s.problems = append(s.problems, problems...)
		return
	}
	defer func() {
		s.problems = slices.DeleteFunc(s.problems, func(p Problem) bool { return skip != "" && unindexed(p.Field) == unindexed(skip) })
		s.problems = append(s.problems, problems...)
	}()
	s.at = positions(src)
	if !u.bundled {
		s.refuseBundledOnly(m)
	}
	d := s.define(u.name, m)
	d.Source = u.source
	d.Bundled = u.bundled
	s.validate(d)
	u.programs = s.compile(d)
	u.programs.ActionTexts = s.parseActionTexts(d.Action)
	if body != nil {
		u.tmpl = s.parseMain(d, base, tmplFile, string(body))
	}
	u.def, u.defined = d, true
}

// crossCheck applies the rules that span manifests: profile references, keys and flags.
// Bundled intents come first, so a user intent is the one that yields; a broken
// intent is checked after every clean one and claims no key, flag or workspace.
func crossCheck(units []*unit) {
	ordered := slices.Clone(units)
	slices.SortStableFunc(ordered, func(a, b *unit) int {
		if a.bundled != b.bundled {
			if a.bundled {
				return -1
			}
			return 1
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
			s.add(fmt.Sprintf("also_profiles[%d].profile", i), "%s is not an intent with judgment blocks", a.Profile)
		}
		if a.Define != "" && u.tmpl != nil && u.tmpl.Lookup(a.Define) == nil {
			s.add(fmt.Sprintf("also_profiles[%d].define", i), "no template defines %s", a.Define)
		}
	}
}

// refusesBrokenProfiles breaks, until none is left, every clean intent whose
// profile or also_profiles names a broken one, since that one's blocks are skipped.
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
					u.sheet.add(fmt.Sprintf("also_profiles[%d].profile", i), "%s does not load; fix its manifest, or name another intent", a.Profile)
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
	return &claims{keys: keys, flags: map[string]meaning{}, drivers: map[string]dintent.Name{}}
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
			s.add("key", "%v", err)
		default:
			u.def.Key = ""
			u.keyProblems = append(u.keyProblems, s.problem("key", "%v; the intent loads without a key (palette only)", err))
		}
	}
	if d.DrivesRow {
		if owner, taken := c.drivers[d.Workspace]; taken {
			s.add("drives_row", "%s drives the rows of %s workspaces already; at most one intent per workspace", owner, d.Workspace)
		} else {
			take = append(take, func() { c.drivers[d.Workspace] = d.Name })
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
