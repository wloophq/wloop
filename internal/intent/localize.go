package intent

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strconv"
	"text/template"

	"github.com/wloophq/wloop/internal/i18n"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/profile"
)

// layerReader is the one way the loaders read a profile file: each text a person reads comes out in the
// reader's locale, by the layer rule of profile.Texts, and every other byte as the layer holds it.
type layerReader struct {
	bundled fs.FS
	locale  string
	// texts is nil when no layer holds the locale's texts: every file reads as written.
	texts    *profile.Texts
	problems *Problems
}

// newLayerReader reads layers, the bundled one from bundled, in locale.
func newLayerReader(locale string, layers []profile.Layer, bundled fs.FS) layerReader {
	r := layerReader{bundled: bundled, locale: locale, problems: &Problems{}}
	if t := profile.NewTexts(layers, bundled, locale, textKey); t.Translates() {
		r.texts = t
	}
	return r
}

// readerOf reads bundled and the layers as they are written, in English.
func readerOf(bundled fs.FS) layerReader {
	return layerReader{bundled: bundled, locale: i18n.Default, problems: &Problems{}}
}

// textKey says whether the key at path of rel holds a text a person reads, as the grammar marks it.
func textKey(rel string, path []string) bool {
	s, ok := builtin.SurfaceOf(rel)
	return ok && builtin.Localized(s, path)
}

// localizedFile says whether rel is a whole-file text a person reads.
func localizedFile(rel string) bool {
	dir, name := path.Split(rel)
	parent, _ := path.Split(path.Clean(dir))
	return parent == "workflows/" && slices.Contains(builtin.LocalizedFiles, name)
}

// read is rel of layer l, the bundled layer read from r's bundled FS, localized.
func (r layerReader) read(l profile.Layer, rel string) (profile.Resolved, error) {
	if l.Bundled() {
		body, err := r.readBundled(rel)
		return profile.Resolved{Layer: l, Path: rel, Body: body}, err
	}
	res, err := profile.ReadFile(l, rel)
	if err != nil {
		return res, err
	}
	res.Body = r.localize(l, rel, res.Body)
	return res, nil
}

// readBundled is rel of the bundled FS, localized; an error wraps fs.ErrNotExist when the FS is nil.
func (r layerReader) readBundled(rel string) ([]byte, error) {
	if r.bundled == nil {
		return nil, fmt.Errorf("no bundled profile: %w", fs.ErrNotExist)
	}
	body, err := fs.ReadFile(r.bundled, rel)
	if err != nil {
		return nil, err
	}
	return r.localize(profile.Layer{Name: "bundled", Source: "embedded"}, rel, body), nil
}

// file is the whole-file text rel resolved across the layers in r's locale; false when r translates no
// whole-file text rel names, and the caller reads its own layer.
func (r layerReader) file(rel string) (profile.Resolved, bool, error) {
	if r.texts == nil || !localizedFile(rel) {
		return profile.Resolved{}, false, nil
	}
	res, err := r.texts.File(rel)
	r.report(r.texts.Problems(rel))
	return res, true, err
}

// localize is src, layer l's file rel, with its texts in r's locale.
func (r layerReader) localize(l profile.Layer, rel string, src []byte) []byte {
	if r.texts == nil {
		return src
	}
	if _, ok := builtin.SurfaceOf(rel); !ok {
		return src
	}
	out, problems := r.texts.Localize(l, rel, src)
	r.report(problems)
	return out
}

func (r layerReader) report(ps profile.Problems) {
	for _, p := range ps {
		*r.problems = append(*r.problems, Problem{File: p.File, Field: p.Key, Msg: p.Msg})
	}
}

// funcs are the template functions of r's texts.
func (r layerReader) funcs() template.FuncMap { return TextFuncs(r.locale) }

// TextFuncs are the template functions of a text localized in locale: the action functions and plural.
func TextFuncs(locale string) template.FuncMap {
	out := maps.Clone(ActionFuncs)
	out["plural"] = func(n any, pairs ...string) (string, error) { return plural(locale, n, pairs) }
	return out
}

// plural is the text of n's CLDR form in locale among pairs, written form then text, "other" required.
func plural(locale string, n any, pairs []string) (string, error) {
	if len(pairs)%2 != 0 {
		return "", fmt.Errorf("plural takes form and text pairs, such as \"one\" \"draft\" \"other\" \"drafts\"")
	}
	forms := i18n.Forms{}
	for i := 0; i < len(pairs); i += 2 {
		if !slices.Contains(builtin.PluralForms, pairs[i]) {
			return "", fmt.Errorf("plural: %q is no plural form; the forms are %s", pairs[i], builtin.OrList(builtin.PluralForms))
		}
		forms[pairs[i]] = pairs[i+1]
	}
	if _, ok := forms["other"]; !ok {
		return "", fmt.Errorf("plural needs an \"other\" text")
	}
	return forms.For(locale, number(n)), nil
}

// number is n as a count; a value that is no number counts as 0, as the samples a text is checked over hold.
func number(n any) float64 {
	switch x := n.(type) {
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case int32:
		return float64(x)
	case uint:
		return float64(x)
	case uint64:
		return float64(x)
	case float64:
		return x
	case float32:
		return float64(x)
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	}
	return 0
}

// ProfileFile is rel resolved through l's layers, a whole-file text a person reads in l's locale.
func (l *Loaded) ProfileFile(rel string) (profile.Resolved, error) {
	if t := l.reader.texts; t != nil && localizedFile(rel) {
		return t.File(rel)
	}
	for _, layer := range slices.Backward(l.layers) {
		res, err := l.reader.read(layer, rel)
		if !errors.Is(err, fs.ErrNotExist) {
			return res, err
		}
	}
	return profile.Resolved{}, fmt.Errorf("no profile layer holds %s: %w", rel, fs.ErrNotExist)
}

// Locales are English and every locale a layer of layers, the bundled one read from bundled, holds texts in.
func Locales(layers []profile.Layer, bundled fs.FS) []string {
	return profile.NewTexts(layers, bundled, i18n.Default, textKey).Available()
}
