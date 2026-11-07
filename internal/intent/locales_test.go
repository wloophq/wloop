package intent_test

import (
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"testing"
	"text/template"

	"github.com/pelletier/go-toml/v2"

	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/profile"
	"github.com/wloophq/wloop/internal/turn"
)

const ptBRTag = "pt-BR"

// layerFS is the files of a bundled layer: the bundled profile's or a bundled plugin's.
func layerFS(t *testing.T, l profile.Layer) fs.FS {
	t.Helper()
	if l.Bundled() {
		return turn.BundledFS()
	}
	fsys, err := l.FSys()
	if err != nil {
		t.Fatal(err)
	}
	return fsys
}

// texts are the person-read texts of the TOML document src of rel, by dotted key.
func texts(t *testing.T, rel string, src []byte) map[string]string {
	t.Helper()
	var doc map[string]any
	if err := toml.Unmarshal(src, &doc); err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	surface, _ := builtin.SurfaceOf(rel)
	out := map[string]string{}
	var walk func(p []string, v any)
	walk = func(p []string, v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, e := range x {
				walk(append(slices.Clip(p), k), e)
			}
		case []any:
			for i, e := range x {
				walk(append(slices.Clip(p), fmt.Sprintf("[%d]", i)), e)
			}
		case string:
			if builtin.Localized(surface, p) {
				out[strings.Join(p, ".")] = x
			}
		}
	}
	walk(nil, doc)
	return out
}

// draftTexts are the text files of fsys's workflows whose step proposes a draft, which piece E translates.
func draftTexts(t *testing.T, fsys fs.FS) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	dirs, _ := fs.ReadDir(fsys, "workflows")
	for _, d := range dirs {
		b, err := fs.ReadFile(fsys, path.Join("workflows", d.Name(), "workflow.toml"))
		if err != nil {
			continue
		}
		var m struct{ Step []map[string]any }
		_ = toml.Unmarshal(b, &m)
		for _, st := range m.Step {
			if st["use"] == builtin.StepDraftPropose {
				name, _ := st["text"].(string)
				out[path.Join("workflows", d.Name(), cmpOr(name, "text.md.tmpl"))] = true
			}
		}
	}
	return out
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// bundledTextGaps are the texts of layer, as "<file>: <key>", that its locales/<locale>/ mirror lacks;
// strings.toml is left out when skipStrings.
func bundledTextGaps(t *testing.T, l profile.Layer, locale string, skipStrings bool) []string {
	t.Helper()
	fsys := layerFS(t, l)
	drafts := draftTexts(t, fsys)
	var gaps []string
	err := fs.WalkDir(fsys, ".", func(rel string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasPrefix(rel, profile.LocalesDir+"/") {
			return err
		}
		if skipStrings && rel == "strings.toml" {
			return nil
		}
		mirror := path.Join(profile.LocalesDir, locale, rel)
		if dir, name := path.Split(rel); slices.Contains(builtin.LocalizedFiles, name) && strings.HasPrefix(dir, "workflows/") {
			if _, err := fs.Stat(fsys, mirror); err != nil && !drafts[rel] {
				gaps = append(gaps, rel+": the whole file")
			}
			return nil
		}
		if _, ok := builtin.SurfaceOf(rel); !ok {
			return nil
		}
		base, err := fs.ReadFile(fsys, rel)
		if err != nil {
			return err
		}
		have := map[string]string{}
		if b, err := fs.ReadFile(fsys, mirror); err == nil {
			have = texts(t, rel, b)
		}
		for k := range texts(t, rel, base) {
			if _, ok := have[k]; !ok {
				gaps = append(gaps, rel+": "+k)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(gaps)
	return gaps
}

func TestEveryBundledTextHasPtBR(t *testing.T) {
	for _, l := range profile.BundledChain() {
		t.Run(l.Name, func(t *testing.T) {
			if gaps := bundledTextGaps(t, l, ptBRTag, false); len(gaps) > 0 {
				t.Errorf("%d texts have no pt-BR:\n%s", len(gaps), strings.Join(gaps, "\n"))
			}
		})
	}
}

func TestBundledPtBRNamesNoStrayKey(t *testing.T) {
	for _, l := range profile.BundledChain() {
		t.Run(l.Name, func(t *testing.T) {
			fsys := layerFS(t, l)
			root := path.Join(profile.LocalesDir, ptBRTag)
			_ = fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return nil
				}
				rel := strings.TrimPrefix(p, root+"/")
				base, err := fs.ReadFile(fsys, rel)
				if err != nil {
					t.Errorf("%s translates %s, which the layer does not hold", p, rel)
					return nil
				}
				if _, ok := builtin.SurfaceOf(rel); !ok {
					return nil
				}
				loc, _ := fs.ReadFile(fsys, p)
				english := texts(t, rel, base)
				var all map[string]any
				_ = toml.Unmarshal(loc, &all)
				for k := range flatStrings(all) {
					if _, ok := english[k]; !ok {
						t.Errorf("%s: %s names no English text of %s", p, k, rel)
					}
				}
				return nil
			})
		})
	}
}

// flatStrings are every string value of doc by dotted key.
func flatStrings(doc map[string]any) map[string]string {
	out := map[string]string{}
	var walk func(p []string, v any)
	walk = func(p []string, v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, e := range x {
				walk(append(slices.Clip(p), k), e)
			}
		case []any:
			for i, e := range x {
				walk(append(slices.Clip(p), fmt.Sprintf("[%d]", i)), e)
			}
		case string:
			out[strings.Join(p, ".")] = x
		}
	}
	walk(nil, doc)
	return out
}

func TestBundledProfileLoadsInPtBR(t *testing.T) {
	base, err := turn.Base()
	if err != nil {
		t.Fatal(err)
	}
	_, english := intentloadIn("en", base)
	_, problems := intentloadIn(ptBRTag, base)
	if len(problems) != len(english) {
		t.Errorf("pt-BR loads with problems English has not:\n%s", strings.Join(problems, "\n"))
	}
	for _, p := range problems {
		if strings.Contains(p, "locales/") {
			t.Errorf("%s", p)
		}
	}
}

// intentloadIn is the problems of the bundled chain loaded in locale, as text.
func intentloadIn(locale string, base *template.Template) (*intentload.Loaded, []string) {
	l, problems := intentload.LoadIn(locale, profile.BundledChain(), turn.BundledFS(), base, turn.CheckTemplate)
	var out []string
	for _, p := range problems {
		out = append(out, intentload.Problems{p}.Error())
	}
	return l, out
}
