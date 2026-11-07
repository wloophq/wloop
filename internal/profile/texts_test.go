package profile

import (
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

func textKeys(rel string, path []string) bool {
	last := path[len(path)-1]
	switch rel {
	case "strings.toml":
		return len(path) == 2 && path[0] == "strings"
	case "workflows/w/workflow.toml":
		return last == "summary" || last == "ask"
	}
	return last == "label" || last == "confirm"
}

func mapFS(files map[string]string) fstest.MapFS {
	out := fstest.MapFS{}
	for name, body := range files {
		out[name] = &fstest.MapFile{Data: []byte(body)}
	}
	return out
}

var bundledActions = map[string]string{
	"actions.toml":                            "[pause]\nlabel = \"Pause\"\nwhen = \"x\"\n\n[resume]\nlabel = \"Resume\"\n",
	"locales/pt-BR/actions.toml":              "[pause]\nlabel = \"Pausar\"\n",
	"workflows/w/workflow.toml":               "summary = \"Ask\"\nask = \"Go?\"\n",
	"locales/pt-BR/workflows/w/workflow.toml": "summary = \"Perguntar\"\nask = \"Seguir?\"\n",
	"workflows/w/text.md.tmpl":                "Hi",
	"locales/pt-BR/workflows/w/text.md.tmpl":  "Oi",
}

func newTexts(t *testing.T, locale string, users ...map[string]string) *Texts {
	t.Helper()
	layers := []Layer{bundled()}
	for i, u := range users {
		if i == 0 && len(users) == 2 {
			layers = append(layers, Layer{Name: "plugin:acme", Source: "plugins/acme/profile", FS: mapFS(u)})
			continue
		}
		layers = append(layers, user(dir(t, u)))
	}
	return NewTexts(layers, mapFS(bundledActions), locale, textKeys)
}

func text(t *testing.T, x *Texts, rel string, path ...string) string {
	t.Helper()
	s, ok := x.Text(rel, path)
	if !ok {
		t.Fatalf("%s %q: not found", rel, path)
	}
	return s
}

func TestTextsLayerRule(t *testing.T) {
	label := func(locale string, layers ...map[string]string) string {
		return text(t, newTexts(t, locale, layers...), "actions.toml", "pause", "label")
	}
	copyFile := map[string]string{"actions.toml": "[pause]\nlabel = \"Pause\"\n"}
	hold := map[string]string{"actions.toml": "[pause]\nlabel = \"Hold\"\n"}
	holdPt := map[string]string{"actions.toml": "[pause]\nlabel = \"Hold\"\n", "locales/pt-BR/actions.toml": "[pause]\nlabel = \"Segurar\"\n"}
	plugin := map[string]string{"locales/pt-BR/actions.toml": "[pause]\nlabel = \"Pausar (plugin)\"\n"}
	for _, c := range []struct {
		name, locale, want string
		layers             []map[string]string
	}{
		{"no user file", "pt-BR", "Pausar", nil},
		{"a whole copy", "pt-BR", "Pausar", []map[string]string{copyFile}},
		{"the user's own text", "pt-BR", "Hold", []map[string]string{hold}},
		{"the user's own translation", "pt-BR", "Segurar", []map[string]string{holdPt}},
		{"a plugin's translation under the user's text", "pt-BR", "Hold", []map[string]string{plugin, hold}},
		{"a plugin's translation", "pt-BR", "Pausar (plugin)", []map[string]string{plugin, copyFile}},
		{"a copy in English", "en", "Pause", []map[string]string{copyFile}},
		{"the user's text in English", "en", "Hold", []map[string]string{holdPt}},
		{"an unshipped locale", "fr", "Pause", nil},
		{"a near locale", "pt-PT", "Pausar", nil},
	} {
		if got := label(c.locale, c.layers...); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	if got := text(t, newTexts(t, "pt-BR"), "actions.toml", "resume", "label"); got != "Resume" {
		t.Errorf("a text without pt-BR: %q", got)
	}
	if _, ok := newTexts(t, "pt-BR").Text("actions.toml", []string{"absent", "label"}); ok {
		t.Error("a text no layer holds")
	}
}

func TestTextsResolvePerKeyWhenAFileReplacesAnotherWhole(t *testing.T) {
	x := newTexts(t, "pt-BR", map[string]string{"workflows/w/workflow.toml": "summary = \"Ask\"\nask = \"Proceed?\"\n"})
	if s, a := text(t, x, "workflows/w/workflow.toml", "summary"), text(t, x, "workflows/w/workflow.toml", "ask"); s != "Perguntar" || a != "Proceed?" {
		t.Fatalf("%q %q", s, a)
	}
	l := x.layers[1]
	src := []byte("summary = \"Ask\"\n# kept\nask = \"Proceed?\"\nother = 1\n")
	got, problems := x.Localize(l, "workflows/w/workflow.toml", src)
	if string(got) != "summary = \"Perguntar\"\n# kept\nask = \"Proceed?\"\nother = 1\n" || len(problems) != 0 {
		t.Fatalf("%q %v", got, problems)
	}
}

func TestTextsFileRule(t *testing.T) {
	for _, c := range []struct {
		name, locale, want string
		layers             []map[string]string
	}{
		{"bundled", "pt-BR", "Oi", nil},
		{"a user copy", "pt-BR", "Oi", []map[string]string{{"workflows/w/text.md.tmpl": "Hi"}}},
		{"the user's own", "pt-BR", "Hey", []map[string]string{{"workflows/w/text.md.tmpl": "Hey"}}},
		{"English", "en", "Hi", nil},
	} {
		r, err := newTexts(t, c.locale, c.layers...).File("workflows/w/text.md.tmpl")
		if err != nil || string(r.Body) != c.want {
			t.Errorf("%s: %q %v", c.name, r.Body, err)
		}
	}
	if _, err := newTexts(t, "pt-BR").File("workflows/absent/text.md.tmpl"); err == nil {
		t.Error("a file no layer holds")
	}
}

func TestLocalizeRefusesStrayLocaleKeys(t *testing.T) {
	x := newTexts(t, "pt-BR", map[string]string{"locales/pt-BR/actions.toml": "[pause]\nlable = \"Segurar\"\nwhen = \"y\"\n\n[nope]\nlabel = \"N\"\n"})
	_, problems := x.Localize(x.layers[0], "actions.toml", []byte(bundledActions["actions.toml"]))
	var got []string
	for _, p := range problems {
		got = append(got, p.Key+": "+p.Msg)
		if !strings.HasSuffix(p.File, "locales/pt-BR/actions.toml") || !strings.Contains(p.File, x.layers[1].Dir) {
			t.Errorf("problem names %s", p.File)
		}
	}
	want := []string{
		`pause.lable: names no text a person reads; did you mean "label"?`,
		"pause.when: names no text a person reads",
		"nope.label: no layer at or below this one holds its English text",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("%q", got)
	}
	if got := text(t, x, "actions.toml", "pause", "label"); got != "Pausar" {
		t.Fatalf("%q", got)
	}
	if again := x.Problems("actions.toml"); len(again) != 0 {
		t.Fatalf("reported twice: %v", again)
	}
}

func TestLocalizeRefusesAFieldTheBaseTextDoesNotName(t *testing.T) {
	x := NewTexts([]Layer{bundled(), user(dir(t, map[string]string{
		"locales/pt-BR/actions.toml":             "[mute]\nconfirm = \"Silenciar {{.pr.title}}?\"\n",
		"locales/pt-BR/strings.toml":             "[strings]\n\"{n} of {total}\" = \"{n} de {all}\"\n\"{n} drafts\" = \"{n} rascunhos\"\n",
		"locales/pt-BR/workflows/w/text.md.tmpl": "Oi {{.who}}",
	}))}, mapFS(map[string]string{
		"actions.toml":             "[mute]\nconfirm = \"Mute {{.pr.slug}}?\"\n",
		"strings.toml":             "[strings]\n\"{n} of {total}\" = \"{n} of {total}\"\n\"{n} drafts\" = \"{n} drafts\"\n",
		"workflows/w/text.md.tmpl": "Hi {{.name}}",
	}), "pt-BR", textKeys)
	if got := text(t, x, "actions.toml", "mute", "confirm"); got != "Mute {{.pr.slug}}?" {
		t.Errorf("%q", got)
	}
	if p := x.Problems("actions.toml"); len(p) != 1 || p[0].Key != "mute.confirm" || !strings.Contains(p[0].Msg, ".pr.title") {
		t.Errorf("%+v", p)
	}
	if got := text(t, x, "strings.toml", "strings", "{n} of {total}"); got != "{n} of {total}" {
		t.Errorf("%q", got)
	}
	if got := text(t, x, "strings.toml", "strings", "{n} drafts"); got != "{n} rascunhos" {
		t.Errorf("%q", got)
	}
	if p := x.Problems("strings.toml"); len(p) != 1 || p[0].Key != "strings.{n} of {total}" || !strings.Contains(p[0].Msg, "{all}") {
		t.Errorf("%+v", p)
	}
	if r, _ := x.File("workflows/w/text.md.tmpl"); string(r.Body) != "Hi {{.name}}" {
		t.Errorf("%q", r.Body)
	}
	if p := x.Problems("workflows/w/text.md.tmpl"); len(p) != 1 || !strings.Contains(p[0].Msg, ".who") {
		t.Errorf("%+v", p)
	}
}

func TestAvailableListsEveryLayersLocales(t *testing.T) {
	x := newTexts(t, "en", map[string]string{"locales/es/actions.toml": "", "locales/.hidden/x": ""})
	if got := x.Available(); !slices.Equal(got, []string{"en", "es", "pt-BR"}) {
		t.Fatalf("%q", got)
	}
}
