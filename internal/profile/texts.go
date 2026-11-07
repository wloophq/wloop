package profile

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"text/template/parse"

	"github.com/wloophq/wloop/internal/i18n"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
)

// LocalesDir holds a layer's translations, locales/<tag>/<rel> mirroring each localizable file rel.
const LocalesDir = "locales"

// TextKeys says whether the key at path of profile file rel holds a text a person reads.
type TextKeys func(rel string, path []string) bool

// TextProblem is a locale file's key Texts refuses.
type TextProblem struct{ File, Key, Msg string }

// Problems are the TextProblems of a localization.
type Problems []TextProblem

// Texts resolves the texts a person reads, per key and per locale, across a profile's layers.
//
// For each text the walk goes from the highest layer down: a layer that holds the text in the locale wins;
// a layer whose English text differs from the one below it wins with that text; the bottom layer ends with
// its locale text, else its English one. The keys of a plugin's file Localize reads in its own layer or in
// the user's layer of it walk only the layers of no plugin and that plugin's: plugins share file names such as
// ui.toml, never texts. Every other read, Text, Forms and the whole files, walks every layer.
type Texts struct {
	layers  []Layer
	bundled fs.FS
	locale  string
	dir     string
	keys    TextKeys

	mu       sync.Mutex
	files    map[string]*fileTexts
	reported map[string]bool
}

// fileTexts are one profile file's texts in every layer.
type fileTexts struct {
	base, loc []map[string]string // by layer, then by path key
	// baseForms and locForms are the texts written as a table of CLDR plural forms, by layer, then by the
	// path key of the table.
	baseForms, locForms []map[string]map[string]string
	whole, wholeLoc     []*Resolved // by layer, of a whole-file text
	problems            Problems
}

// NewTexts resolves the texts keys names across layers, the bundled layer reading bundled, in locale.
func NewTexts(layers []Layer, bundled fs.FS, locale string, keys TextKeys) *Texts {
	t := &Texts{layers: layers, bundled: bundled, locale: locale, keys: keys, files: map[string]*fileTexts{}, reported: map[string]bool{}}
	if m := i18n.Match([]string{locale}, t.Available()); t.holds(m) {
		t.dir = m
	}
	return t
}

// Locale is the locale t resolves texts in.
func (t *Texts) Locale() string { return t.locale }

// Translates says whether some layer holds texts in t's locale, or in the one nearest it; when false every
// text reads as its layers write it.
func (t *Texts) Translates() bool { return t.dir != "" }

// Localize is src, layer l's file rel, with each text key's value replaced by the text l's level resolves to:
// the layer rule over l and the layers below it, and the translations of the layers above it for the texts
// they hold no English of. Its problems are those of rel's locale files in every layer, reported by the first
// of Localize and Problems to read rel.
func (t *Texts) Localize(l Layer, rel string, src []byte) ([]byte, Problems) {
	at := t.index(l)
	strs, err := tomlStrings(src)
	if err != nil {
		return src, t.Problems(rel)
	}
	values := map[string]string{}
	for _, s := range strs {
		if !t.keys(rel, s.path) {
			continue
		}
		if v, ok := t.textAt(rel, s.path, at, l.PluginID()); ok && v != s.text {
			values[pathKey(s.path)] = v
		}
	}
	out, err := Splice(src, values)
	if err != nil {
		return src, t.Problems(rel)
	}
	return out, t.Problems(rel)
}

// Problems are the problems of rel's locale files in every layer, the first time any read of t asks for
// them; nil after.
func (t *Texts) Problems(rel string) Problems {
	f := t.file(rel)
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.reported[rel] {
		return nil
	}
	t.reported[rel] = true
	return f.problems
}

// index is l's place among t's layers by its name; the top one when t has no such layer.
func (t *Texts) index(l Layer) int {
	for i, x := range t.layers {
		if x.Name == l.Name {
			return i
		}
	}
	return len(t.layers) - 1
}

// Text is the text at path of rel in t's locale, by the layer rule; false when no layer holds it.
func (t *Texts) Text(rel string, path []string) (string, bool) {
	return t.textAt(rel, path, len(t.layers)-1, "")
}

// visible says whether layer j takes part in a walk of plugin scope's file: every layer for the scope of no
// plugin, else the layers of no plugin and of that plugin.
func (t *Texts) visible(scope string, j int) bool {
	id := t.layers[j].PluginID()
	return scope == "" || id == "" || id == scope
}

// textAt is Text as layer at's level reads it in plugin scope's chain: a layer above at gives only a
// translation of a text it holds no English of.
func (t *Texts) textAt(rel string, path []string, at int, scope string) (string, bool) {
	f := t.file(rel)
	k := pathKey(path)
	for i := len(t.layers) - 1; i >= 0; i-- {
		if !t.visible(scope, i) {
			continue
		}
		s, own := f.base[i][k]
		if loc, ok := f.loc[i][k]; ok && (i <= at || !own) {
			return loc, true
		}
		if !own || i > at {
			continue
		}
		below, held := t.baseBelow(f, i, k, scope)
		if i == 0 || !held || below != s {
			return s, true
		}
	}
	return "", false
}

func (t *Texts) baseBelow(f *fileTexts, i int, k, scope string) (string, bool) {
	for j := i - 1; j >= 0; j-- {
		if s, ok := f.base[j][k]; ok && t.visible(scope, j) {
			return s, true
		}
	}
	return "", false
}

// File is the whole-file text rel in t's locale, by the same rule.
func (t *Texts) File(rel string) (Resolved, error) {
	return t.FileAt(t.layers[len(t.layers)-1], rel)
}

// FileAt is File as layer l's level reads it, the way Localize reads a key. A whole-file text lives under its
// workflow's directory, whose name no two plugins share, so the walk reads every layer, whatever plugin l is.
func (t *Texts) FileAt(l Layer, rel string) (Resolved, error) {
	f := t.file(rel)
	at := t.index(l)
	for i := len(t.layers) - 1; i >= 0; i-- {
		r := f.whole[i]
		if loc := f.wholeLoc[i]; loc != nil && (i <= at || r == nil) {
			return *loc, nil
		}
		if r == nil || i > at {
			continue
		}
		var below *Resolved
		for j := i - 1; j >= 0 && below == nil; j-- {
			below = f.whole[j]
		}
		if below == nil || !bytes.Equal(below.Body, r.Body) {
			return *r, nil
		}
	}
	return Resolved{}, fmt.Errorf("no profile layer holds %s: %w", rel, fs.ErrNotExist)
}

// Available are "en" and every locale any layer holds a locales/<tag>/ directory of.
func (t *Texts) Available() []string {
	out := []string{i18n.Default}
	for _, l := range t.layers {
		fsys, err := t.fsys(l)
		if err != nil {
			continue
		}
		entries, _ := fs.ReadDir(fsys, LocalesDir)
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && !slices.Contains(out, e.Name()) {
				out = append(out, e.Name())
			}
		}
	}
	slices.Sort(out[1:])
	return out
}

func (t *Texts) holds(tag string) bool {
	return tag != i18n.Default || slices.Contains(t.Available()[1:], i18n.Default)
}

func (t *Texts) fsys(l Layer) (fs.FS, error) {
	if l.Bundled() {
		if t.bundled == nil {
			return nil, fs.ErrNotExist
		}
		return t.bundled, nil
	}
	return l.FSys()
}

func (t *Texts) read(l Layer, rel string) (*Resolved, error) {
	fsys, err := t.fsys(l)
	if err != nil {
		return nil, err
	}
	body, err := fs.ReadFile(fsys, rel)
	if err != nil {
		return nil, err
	}
	return &Resolved{Layer: l, Path: l.FileName(rel), Body: body}, nil
}

// file reads rel and its locale mirror from every layer, once.
func (t *Texts) file(rel string) *fileTexts {
	t.mu.Lock()
	defer t.mu.Unlock()
	if f, ok := t.files[rel]; ok {
		return f
	}
	n := len(t.layers)
	f := &fileTexts{base: make([]map[string]string, n), loc: make([]map[string]string, n), whole: make([]*Resolved, n), wholeLoc: make([]*Resolved, n),
		baseForms: make([]map[string]map[string]string, n), locForms: make([]map[string]map[string]string, n)}
	toml := path.Ext(rel) == ".toml"
	for i, l := range t.layers {
		base, err := t.read(l, rel)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			f.problems = append(f.problems, TextProblem{File: l.FileName(rel), Msg: err.Error()})
		}
		if !toml {
			f.whole[i] = base
		} else if base != nil {
			f.base[i], f.baseForms[i] = t.flatten(rel, base.Body)
		}
		if t.dir == "" {
			continue
		}
		locRel := path.Join(LocalesDir, t.dir, rel)
		loc, err := t.read(l, locRel)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				f.problems = append(f.problems, TextProblem{File: l.FileName(locRel), Msg: err.Error()})
			}
			continue
		}
		if !toml {
			if msg := fieldsProblem(string(loc.Body), string(baseOf(f.whole[:i+1]))); msg != "" {
				f.problems = append(f.problems, TextProblem{File: loc.Path, Msg: msg})
				continue
			}
			loc.Locale = t.dir
			f.wholeLoc[i] = loc
			continue
		}
		f.loc[i], f.locForms[i] = t.checkLocale(f, i, rel, loc)
	}
	t.files[rel] = f
	return f
}

func baseOf(whole []*Resolved) []byte {
	for _, r := range slices.Backward(whole) {
		if r != nil {
			return r.Body
		}
	}
	return nil
}

// flatten is the text keys of the TOML document src of rel, and its texts written as a table of plural forms;
// a document that does not parse holds none, its loader reports why.
func (t *Texts) flatten(rel string, src []byte) (map[string]string, map[string]map[string]string) {
	strs, err := tomlStrings(src)
	if err != nil {
		return nil, nil
	}
	out, forms := map[string]string{}, map[string]map[string]string{}
	for _, s := range strs {
		if unit, form, ok := t.formOf(rel, s.path); ok {
			if forms[unit] == nil {
				forms[unit] = map[string]string{}
			}
			forms[unit][form] = s.text
		} else if t.keys(rel, s.path) {
			out[pathKey(s.path)] = s.text
		}
	}
	return out, forms
}

// formOf says whether path is a form of a text written as a table: a key whose parent is itself a text.
func (t *Texts) formOf(rel string, path []string) (unit, form string, ok bool) {
	if len(path) < 2 || !t.keys(rel, path[:len(path)-1]) {
		return "", "", false
	}
	return pathKey(path[:len(path)-1]), path[len(path)-1], true
}

// value is the text at key k of layer i's base, or of its locale file when loc, as plural forms: a string is
// its "other" form.
func (f *fileTexts) value(i int, k string, loc bool) (map[string]string, bool) {
	scalars, forms := f.base, f.baseForms
	if loc {
		scalars, forms = f.loc, f.locForms
	}
	if v, ok := forms[i][k]; ok {
		return v, true
	}
	if s, ok := scalars[i][k]; ok {
		return map[string]string{"other": s}, true
	}
	return nil, false
}

// Forms is the text at path of rel in t's locale as its CLDR plural forms, by the layer rule, the whole text
// resolved as one: a translation may name forms its English text has not, or be plural where the English
// text is a string; false when no layer holds it.
func (t *Texts) Forms(rel string, path []string) (map[string]string, bool) {
	return t.formsIn("", rel, path)
}

// PluginForms is Forms of plugin id's file rel, walking only the layers of no plugin and of id.
func (t *Texts) PluginForms(id, rel string, path []string) (map[string]string, bool) {
	return t.formsIn(id, rel, path)
}

func (t *Texts) formsIn(scope, rel string, path []string) (map[string]string, bool) {
	f := t.file(rel)
	k := pathKey(path)
	for i := len(t.layers) - 1; i >= 0; i-- {
		if !t.visible(scope, i) {
			continue
		}
		if v, ok := f.value(i, k, true); ok {
			return v, true
		}
		v, own := f.value(i, k, false)
		if !own {
			continue
		}
		var below map[string]string
		held := false
		for j := i - 1; j >= 0 && !held; j-- {
			if t.visible(scope, j) {
				below, held = f.value(j, k, false)
			}
		}
		if i == 0 || !held || !maps.Equal(below, v) {
			return v, true
		}
	}
	return nil, false
}

// checkLocale is the texts of layer i's locale file of rel that name a text and only the fields its English
// text names, and its texts written as plural forms; each other key is a problem.
func (t *Texts) checkLocale(f *fileTexts, i int, rel string, loc *Resolved) (map[string]string, map[string]map[string]string) {
	strs, err := tomlStrings(loc.Body)
	if err != nil {
		f.problems = append(f.problems, TextProblem{File: loc.Path, Msg: err.Error()})
		return nil, nil
	}
	scope := t.layers[i].PluginID()
	english := func(k string) (string, bool) {
		for j := i; j >= 0; j-- {
			if !t.visible(scope, j) {
				continue
			}
			if v, ok := f.value(j, k, false); ok {
				var all []string
				for _, s := range v {
					all = append(all, s)
				}
				return strings.Join(all, "\n"), true
			}
		}
		return "", false
	}
	out, forms := map[string]string{}, map[string]map[string]string{}
	refused := map[string]bool{}
	for _, s := range strs {
		k := pathKey(s.path)
		problem := func(msg string) {
			f.problems = append(f.problems, TextProblem{File: loc.Path, Key: keyString(s.path), Msg: msg})
		}
		if unit, form, ok := t.formOf(rel, s.path); ok {
			base, held := english(unit)
			switch {
			case !held:
				problem("no layer at or below this one holds its English text")
			case !slices.Contains(builtin.PluralForms, form):
				problem(fmt.Sprintf("%q is no plural form; the forms are %s", form, builtin.OrList(builtin.PluralForms)))
			default:
				if msg := fieldsProblem(s.text, base); msg != "" {
					problem(msg)
				} else {
					if forms[unit] == nil {
						forms[unit] = map[string]string{}
					}
					forms[unit][form] = s.text
					continue
				}
			}
			refused[unit] = true
			continue
		}
		base, held := english(k)
		switch {
		case !t.keys(rel, s.path):
			problem("names no text a person reads" + t.near(f, i, s.path))
		case !held:
			problem("no layer at or below this one holds its English text")
		default:
			if msg := fieldsProblem(s.text, base); msg != "" {
				problem(msg)
				continue
			}
			out[k] = s.text
		}
	}
	for unit, v := range forms {
		if _, ok := v["other"]; ok && !refused[unit] {
			continue
		}
		if _, ok := v["other"]; !ok {
			f.problems = append(f.problems, TextProblem{File: loc.Path, Key: keyString(strings.Split(unit, "\x00")), Msg: "needs other: the text of every count no other form names"})
		}
		delete(forms, unit)
	}
	return out, forms
}

// near is the did-you-mean of a locale key among its siblings' English texts.
func (t *Texts) near(f *fileTexts, i int, p []string) string {
	prefix := pathKey(p[:len(p)-1])
	var siblings []string
	for j := i; j >= 0; j-- {
		if !t.visible(t.layers[i].PluginID(), j) {
			continue
		}
		for k := range f.base[j] {
			if parent, last := cutLast(k); parent == prefix && !slices.Contains(siblings, last) {
				siblings = append(siblings, last)
			}
		}
	}
	slices.Sort(siblings)
	return expr.DidYouMean(p[len(p)-1], siblings)
}

// cutLast is path key k's parent key and last segment.
func cutLast(k string) (string, string) {
	i := strings.LastIndex(k, "\x00")
	if i < 0 {
		return "", k
	}
	return k[:i], k[i+1:]
}

// fieldsProblem says why text names a field or placeholder base does not, "" when it names none.
func fieldsProblem(text, base string) string {
	got, err := fieldsOf(text)
	if err != nil {
		return "does not parse: " + err.Error()
	}
	want, _ := fieldsOf(base)
	for _, f := range got {
		if !slices.Contains(want, f) {
			return fmt.Sprintf("names %s, which the English text does not", f)
		}
	}
	return ""
}

var placeholder = regexp.MustCompile(`\{[A-Za-z_][A-Za-z0-9_]*\}`)

// fieldsOf are what a template text reaches: its field and variable chains ({{.pr.slug}} as .pr.slug), the
// functions it calls and the templates it runs; or the {name} placeholders of a plain text.
func fieldsOf(text string) ([]string, error) {
	if !strings.Contains(text, "{{") {
		return placeholder.FindAllString(text, -1), nil
	}
	tr := parse.New("text")
	tr.Mode = parse.SkipFuncCheck
	if _, err := tr.Parse(text, "{{", "}}", map[string]*parse.Tree{}); err != nil {
		return nil, err
	}
	var out []string
	var walk func(n parse.Node)
	walk = func(n parse.Node) {
		switch x := n.(type) {
		case *parse.ListNode:
			if x != nil {
				for _, c := range x.Nodes {
					walk(c)
				}
			}
		case *parse.ActionNode:
			walk(x.Pipe)
		case *parse.PipeNode:
			if x != nil {
				for _, c := range x.Cmds {
					walk(c)
				}
			}
		case *parse.CommandNode:
			for _, a := range x.Args {
				walk(a)
			}
		case *parse.FieldNode:
			out = append(out, "."+strings.Join(x.Ident, "."))
		case *parse.VariableNode:
			out = append(out, strings.Join(x.Ident, "."))
		case *parse.ChainNode:
			walk(x.Node)
			out = append(out, "(…)."+strings.Join(x.Field, "."))
		case *parse.IdentifierNode:
			out = append(out, "function "+x.Ident)

		case *parse.IfNode:
			walk(x.Pipe)
			walk(x.List)
			walk(x.ElseList)
		case *parse.RangeNode:
			walk(x.Pipe)
			walk(x.List)
			walk(x.ElseList)
		case *parse.WithNode:
			walk(x.Pipe)
			walk(x.List)
			walk(x.ElseList)
		case *parse.TemplateNode:
			out = append(out, fmt.Sprintf("template %q", x.Name))
			walk(x.Pipe)
		}
	}
	walk(tr.Root)
	return out, nil
}
