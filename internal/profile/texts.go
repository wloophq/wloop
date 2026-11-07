package profile

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"text/template/parse"

	"github.com/wloophq/wloop/internal/i18n"
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
// its locale text, else its English one.
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
	base, loc       []map[string]string // by layer, then by path key
	whole, wholeLoc []*Resolved         // by layer, of a whole-file text
	problems        Problems
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

// Localize is src, layer l's file rel, with each text key's value replaced by Text(rel, path). Its problems
// are those of rel's locale files in every layer, reported by the first of Localize and Problems to read rel.
func (t *Texts) Localize(l Layer, rel string, src []byte) ([]byte, Problems) {
	strs, err := tomlStrings(src)
	if err != nil {
		return src, t.Problems(rel)
	}
	values := map[string]string{}
	for _, s := range strs {
		if !t.keys(rel, s.path) {
			continue
		}
		if v, ok := t.Text(rel, s.path); ok && v != s.text {
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

// Text is the text at path of rel in t's locale, by the layer rule; false when no layer holds it.
func (t *Texts) Text(rel string, path []string) (string, bool) {
	f := t.file(rel)
	k := pathKey(path)
	for i := len(t.layers) - 1; i >= 0; i-- {
		if s, ok := f.loc[i][k]; ok {
			return s, true
		}
		s, ok := f.base[i][k]
		if !ok {
			continue
		}
		below, held := f.baseBelow(i, k)
		if i == 0 || !held || below != s {
			return s, true
		}
	}
	return "", false
}

func (f *fileTexts) baseBelow(i int, k string) (string, bool) {
	for j := i - 1; j >= 0; j-- {
		if s, ok := f.base[j][k]; ok {
			return s, true
		}
	}
	return "", false
}

// File is the whole-file text rel in t's locale, by the same rule.
func (t *Texts) File(rel string) (Resolved, error) {
	f := t.file(rel)
	for i := len(t.layers) - 1; i >= 0; i-- {
		if r := f.wholeLoc[i]; r != nil {
			return *r, nil
		}
		r := f.whole[i]
		if r == nil {
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
	f := &fileTexts{base: make([]map[string]string, n), loc: make([]map[string]string, n), whole: make([]*Resolved, n), wholeLoc: make([]*Resolved, n)}
	toml := path.Ext(rel) == ".toml"
	for i, l := range t.layers {
		base, err := t.read(l, rel)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			f.problems = append(f.problems, TextProblem{File: l.FileName(rel), Msg: err.Error()})
		}
		if !toml {
			f.whole[i] = base
		} else if base != nil {
			f.base[i] = t.flatten(rel, base.Body)
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
			f.wholeLoc[i] = loc
			continue
		}
		f.loc[i] = t.checkLocale(f, i, rel, loc)
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

// flatten is the text keys of the TOML document src of rel; a document that does not parse holds none, its
// loader reports why.
func (t *Texts) flatten(rel string, src []byte) map[string]string {
	strs, err := tomlStrings(src)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, s := range strs {
		if t.keys(rel, s.path) {
			out[pathKey(s.path)] = s.text
		}
	}
	return out
}

// checkLocale is the texts of layer i's locale file of rel that name a text and only the fields its English
// text names; each other key is a problem.
func (t *Texts) checkLocale(f *fileTexts, i int, rel string, loc *Resolved) map[string]string {
	strs, err := tomlStrings(loc.Body)
	if err != nil {
		f.problems = append(f.problems, TextProblem{File: loc.Path, Msg: err.Error()})
		return nil
	}
	out := map[string]string{}
	for _, s := range strs {
		k := pathKey(s.path)
		var base string
		var held bool
		for j := i; j >= 0 && !held; j-- {
			base, held = f.base[j][k]
		}
		switch {
		case !t.keys(rel, s.path):
			f.problems = append(f.problems, TextProblem{File: loc.Path, Key: keyString(s.path), Msg: "names no text a person reads" + t.near(f, i, s.path)})
		case !held:
			f.problems = append(f.problems, TextProblem{File: loc.Path, Key: keyString(s.path), Msg: "no layer at or below this one holds its English text"})
		default:
			if msg := fieldsProblem(s.text, base); msg != "" {
				f.problems = append(f.problems, TextProblem{File: loc.Path, Key: keyString(s.path), Msg: msg})
				continue
			}
			out[k] = s.text
		}
	}
	return out
}

// near is the did-you-mean of a locale key among its siblings' English texts.
func (t *Texts) near(f *fileTexts, i int, p []string) string {
	prefix := pathKey(p[:len(p)-1])
	var siblings []string
	for j := i; j >= 0; j-- {
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

// fieldsOf are the field chains a template text reads ({{.pr.slug}} as .pr.slug), or the {name}
// placeholders of a plain text.
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
			walk(x.Pipe)
		}
	}
	walk(tr.Root)
	return out, nil
}
