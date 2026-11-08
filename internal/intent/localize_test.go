package intent

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"testing/synctest"
	"text/template"

	"github.com/wloophq/wloop/internal/profile"
)

// bundledWith is the bundled profile with files added or replaced.
func bundledWith(t *testing.T, files map[string]string) fstest.MapFS {
	t.Helper()
	out := fstest.MapFS{}
	err := fs.WalkDir(bundledDefault(t), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(bundledDefault(t), p)
		out[p] = &fstest.MapFile{Data: b}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		out[name] = &fstest.MapFile{Data: []byte(body)}
	}
	return out
}

// userDir is a user profile directory holding files.
func userDir(t *testing.T, files map[string]string) string {
	t.Helper()
	d := t.TempDir()
	for name, body := range files {
		f := filepath.Join(d, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

var ptBR = map[string]string{
	"locales/pt-BR/actions.toml":                         "[pause]\nlabel = \"Pausar\"\n",
	"locales/pt-BR/views.toml":                           "[lens]\nno_group = \"Nenhum\"\n",
	"locales/pt-BR/workflows/answer-unread/text.md.tmpl": "Oi {{index .Facts \"session.repo\"}}",
}

func bundledOnly() []profile.Layer { return []profile.Layer{{Name: "bundled", Source: "embedded"}} }

func TestLoadInServesPtBRTexts(t *testing.T) {
	bundled := bundledWith(t, ptBR)
	for locale, want := range map[string][2]string{"pt-BR": {"Pausar", "Nenhum"}, "pt-PT": {"Pausar", "Nenhum"}, "en": {"Pause", "None"}, "fr": {"Pause", "None"}} {
		l, _ := LoadIn(locale, bundledOnly(), bundled, testBase(), nil)
		if l.Locale != locale || l.Actions["pause"].Label != want[0] || l.Views.Lens.NoGroup != want[1] {
			t.Errorf("%s: locale %q, %q, %q", locale, l.Locale, l.Actions["pause"].Label, l.Views.Lens.NoGroup)
		}
	}
}

func TestLoadInKeepsProblemPositions(t *testing.T) {
	bundled := bundledWith(t, ptBR)
	dir := userDir(t, map[string]string{
		ViewsFile:                  "empty = \"\"\"\nNothing\nin {{.label}}\nhere.\"\"\"\ndefault_sort = \"View order\"\n\n\n\nbogus = 1\n",
		"locales/pt-BR/views.toml": "empty = \"Nada em {{.label}}\"\n",
	})
	for _, locale := range []string{"en", "pt-BR"} {
		l, problems := LoadIn(locale, userLayer(dir), bundled, testBase(), nil)
		var found bool
		for _, p := range problems {
			if p.Field == "bogus" {
				found = true
				if p.Line != 9 {
					t.Errorf("%s: bogus at line %d", locale, p.Line)
				}
			}
		}
		if !found {
			t.Errorf("%s: no problem for bogus: %v", locale, problems)
		}
		var b strings.Builder
		_ = l.Views.Empty.Execute(&b, map[string]any{"label": "x"})
		if want := map[string]string{"en": "Nothing\nin x\nhere.", "pt-BR": "Nada em x"}[locale]; b.String() != want {
			t.Errorf("%s: empty %q", locale, b.String())
		}
	}
}

func TestPluralFuncPicksTheLocalesForm(t *testing.T) {
	render := func(locale, src string, n any) string {
		var b strings.Builder
		if err := template.Must(template.New("t").Funcs(TextFuncs(locale)).Parse(src)).Execute(&b, map[string]any{"n": n}); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	pt := `{{.n}} {{plural .n "one" "pergunta" "other" "perguntas"}}`
	en := `{{.n}} {{plural .n "one" "question" "other" "questions"}}`
	for _, c := range []struct {
		locale, src string
		n           any
		want        string
	}{
		{"pt-BR", pt, 0, "0 pergunta"}, {"pt-BR", pt, 1, "1 pergunta"}, {"pt-BR", pt, 2, "2 perguntas"}, {"pt-BR", pt, int64(3), "3 perguntas"},
		{"en", en, 0, "0 questions"}, {"en", en, 1, "1 question"}, {"en", en, 1.5, "1.5 questions"}, {"en", en, "1", "1 question"},
	} {
		if got := render(c.locale, c.src, c.n); got != c.want {
			t.Errorf("%s %v: %q, want %q", c.locale, c.n, got, c.want)
		}
	}
	dir := userDir(t, map[string]string{
		ViewsFile:                  "[lens]\nquestions = '? {{.n}} {{plural .n \"one\" \"question\" \"other\" \"questions\"}}'\n",
		"locales/pt-BR/views.toml": "[lens]\nquestions = '? {{.n}} {{plural .n \"one\" \"pergunta\" \"other\" \"perguntas\"}}'\n",
	})
	for _, locale := range []string{"en", "pt-BR"} {
		_, problems := LoadIn(locale, userLayer(dir), bundledWith(t, ptBR), testBase(), nil)
		for _, p := range problems {
			if strings.Contains(p.Field, "questions") || strings.Contains(p.File, "views.toml") {
				t.Errorf("%s: %v", locale, p)
			}
		}
	}
}

func TestPluralFuncWithoutOtherIsAProblem(t *testing.T) {
	for src, want := range map[string]string{
		`{{plural 1 "one" "x"}}`:                `plural needs an "other" text`,
		`{{plural 1 "one"}}`:                    "plural takes form and text pairs",
		`{{plural 1 "single" "x" "other" "y"}}`: `"single" is no plural form`,
	} {
		dir := userDir(t, map[string]string{ActionsFile: "[pause]\nconfirm = '" + src + "'\n"})
		_, problems := LoadIn("pt-BR", userLayer(dir), bundledWith(t, ptBR), testBase(), nil)
		var hit bool
		for _, p := range problems {
			hit = hit || p.Field == "pause.confirm" && strings.Contains(p.Msg, want)
		}
		if !hit {
			t.Errorf("%s: %v", src, problems)
		}
	}
}

func TestNotifyTextResolvesPerLocale(t *testing.T) {
	bundled := bundledWith(t, ptBR)
	const rel = "workflows/answer-unread/text.md.tmpl"
	english, _ := fs.ReadFile(bundled, rel)
	for locale, want := range map[string]string{"pt-BR": "Oi {{index .Facts \"session.repo\"}}", "en": string(english)} {
		l, _ := LoadIn(locale, bundledOnly(), bundled, testBase(), nil)
		r, err := l.ProfileFile(rel)
		if err != nil || string(r.Body) != want {
			t.Errorf("%s: %q %v", locale, r.Body, err)
		}
	}
	dir := userDir(t, map[string]string{rel: "Mine"})
	l, _ := LoadIn("pt-BR", userLayer(dir), bundled, testBase(), nil)
	if r, _ := l.ProfileFile(rel); string(r.Body) != "Mine" {
		t.Errorf("the user's own text: %q", r.Body)
	}
	if r, err := l.ProfileFile("workflows/answer-unread/workflow.toml"); err != nil || !strings.Contains(string(r.Body), "notify.self") {
		t.Errorf("a file that is no text: %q %v", r.Body, err)
	}
	if _, err := l.ProfileFile("workflows/answer-unread/absent.md.tmpl"); err == nil {
		t.Error("an absent file resolved")
	}
	drift := userDir(t, map[string]string{"locales/pt-BR/" + rel: "Oi {{.Bogus}}"})
	_, problems := LoadIn("pt-BR", userLayer(drift), bundled, testBase(), nil)
	var hit bool
	for _, p := range problems {
		hit = hit || strings.HasSuffix(p.File, filepath.FromSlash("locales/pt-BR/"+rel)) && strings.Contains(p.Msg, ".Bogus")
	}
	if !hit {
		t.Errorf("a translation naming a field its English text does not: %v", problems)
	}
}

func TestCacheReloadsWhenTheLocaleChanges(t *testing.T) {
	bundled := bundledWith(t, ptBR)
	synctest.Test(t, func(t *testing.T) {
		locale := "en"
		var reloads int
		c := NewCache(bundledOnly, bundled, testBase(), nil, CacheOptions{Locale: func() string { return locale }, OnReload: func() { reloads++ }})
		if l := c.Get(); l.Locale != "en" || l.Actions["pause"].Label != "Pause" {
			t.Fatalf("%q %q", l.Locale, l.Actions["pause"].Label)
		}
		locale = "pt-BR"
		if l := c.Get(); l.Locale != "pt-BR" || l.Actions["pause"].Label != "Pausar" || reloads != 1 {
			t.Fatalf("%q %q, %d reloads", l.Locale, l.Actions["pause"].Label, reloads)
		}
		if c.Get(); reloads != 1 {
			t.Fatalf("an unchanged locale reloaded: %d", reloads)
		}
	})
}

func TestSharedLoadsOneRegistryPerLocale(t *testing.T) {
	s := NewShared(bundledWith(t, ptBR), testBase(), nil)
	pt, _ := s.LoadIn("pt-BR", bundledOnly())
	en, _ := s.Load(bundledOnly())
	again, _ := s.LoadIn("pt-BR", bundledOnly())
	if pt == en || pt != again || pt.Actions["pause"].Label != "Pausar" || en.Actions["pause"].Label != "Pause" {
		t.Fatalf("%q %q, shared %v", pt.Actions["pause"].Label, en.Actions["pause"].Label, pt == again)
	}
}

func TestUserLocaleProblemsReachLoadedProblems(t *testing.T) {
	dir := userDir(t, map[string]string{"locales/pt-BR/actions.toml": "[pause]\nlable = \"Segurar\"\n"})
	l, problems := LoadIn("pt-BR", userLayer(dir), bundledWith(t, ptBR), testBase(), nil)
	var hit int
	for _, p := range problems {
		if strings.HasSuffix(p.File, filepath.Join("locales", "pt-BR", "actions.toml")) && p.Field == "pause.lable" && strings.Contains(p.Msg, `did you mean "label"?`) {
			hit++
		}
	}
	if hit != 1 || l.Actions["pause"].Label != "Pausar" {
		t.Fatalf("%d hits, label %q: %v", hit, l.Actions["pause"].Label, problems)
	}
	if _, problems := LoadIn("en", userLayer(dir), bundledWith(t, ptBR), testBase(), nil); len(problems.only("pause.lable")) != 0 {
		t.Fatalf("an English load read a pt-BR file: %v", problems)
	}
}

func (p Problems) only(field string) Problems {
	var out Problems
	for _, x := range p {
		if x.Field == field {
			out = append(out, x)
		}
	}
	return out
}

func TestARevertedWorkflowReadsTheBundledText(t *testing.T) {
	bundled := bundledWith(t, ptBR)
	manifest, _ := fs.ReadFile(bundled, "workflows/answer-unread/workflow.toml")
	dir := userDir(t, map[string]string{
		"workflows/answer-unread/workflow.toml": "bogus = 1\n" + string(manifest),
		"workflows/answer-unread/text.md.tmpl":  "Mine",
	})
	for locale, want := range map[string]string{"pt-BR": ptBR["locales/pt-BR/workflows/answer-unread/text.md.tmpl"], "en": string(must(fs.ReadFile(bundled, "workflows/answer-unread/text.md.tmpl")))} {
		l, _ := LoadIn(locale, userLayer(dir), bundled, testBase(), nil)
		if len(l.RevertedWorkflows["answer-unread"]) == 0 {
			t.Fatalf("%s: not reverted", locale)
		}
		if r, err := l.ProfileFile("workflows/answer-unread/text.md.tmpl"); err != nil || string(r.Body) != want {
			t.Errorf("%s: %q %v", locale, r.Body, err)
		}
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
