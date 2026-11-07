package intent

import (
	"strings"
	"testing"
)

var stringsFixture = map[string]string{
	StringsFile:                  "[strings]\n\"Open cockpit\" = \"Open cockpit\"\n\"{n} drafts\" = { one = \"{n} draft\", other = \"{n} drafts\" }\n\n[formats]\ntime = \"3:04 PM\"\n",
	"locales/pt-BR/strings.toml": "[strings]\n\"Open cockpit\" = \"Abrir o cockpit\"\n\"{n} drafts\" = { one = \"{n} rascunho\", other = \"{n} rascunhos\" }\n\n[formats]\ntime = \"15:04\"\n",
}

func TestStringsLoadAsFormsAndPlainText(t *testing.T) {
	bundled := bundledWith(t, stringsFixture)
	pt, _ := LoadIn("pt-BR", bundledOnly(), bundled, testBase(), nil)
	if pt.Strings["Open cockpit"]["other"] != "Abrir o cockpit" || len(pt.Strings["Open cockpit"]) != 1 ||
		pt.Strings["{n} drafts"]["one"] != "{n} rascunho" || pt.Strings["{n} drafts"]["other"] != "{n} rascunhos" || pt.Formats["time"] != "15:04" {
		t.Fatalf("%v %v", pt.Strings, pt.Formats)
	}
	en, _ := LoadIn("en", bundledOnly(), bundled, testBase(), nil)
	if en.Strings["Open cockpit"]["other"] != "Open cockpit" || en.Strings["{n} drafts"]["one"] != "{n} draft" || en.Formats["time"] != "3:04 PM" {
		t.Fatalf("%v %v", en.Strings, en.Formats)
	}
}

func TestAUserLayerAddsAndRewordsStrings(t *testing.T) {
	dir := userDir(t, map[string]string{StringsFile: "[strings]\n\"Open cockpit\" = \"Show the cockpit\"\n"})
	l, problems := LoadIn("pt-BR", userLayer(dir), bundledWith(t, stringsFixture), testBase(), nil)
	if got := l.Strings["Open cockpit"]["other"]; got != "Show the cockpit" || len(problems.only("strings.Open cockpit")) != 0 {
		t.Fatalf("%q %v", got, problems)
	}
	if got := l.Strings["{n} drafts"]; got["one"] != "{n} rascunho" {
		t.Fatalf("a key the user left keeps the lower layer's: %v", got)
	}
}

func TestStringsFormsWithoutOtherIsAProblem(t *testing.T) {
	for body, want := range map[string]string{
		"[strings]\n\"x\" = { one = \"x\" }\n":                   "needs other",
		"[strings]\n\"x\" = { other = \"x\", single = \"y\" }\n": "unknown key",
		"[strings]\n\"x\" = 3\n":                                 "string or a table",
		"[strings]\n\"x\" = \"  \"\n":                            "is required",
	} {
		dir := userDir(t, map[string]string{StringsFile: body})
		l, problems := LoadIn("en", userLayer(dir), bundledWith(t, stringsFixture), testBase(), nil)
		var hit bool
		for _, p := range problems {
			hit = hit || strings.HasPrefix(p.Field, "strings.x") && strings.Contains(p.Msg, want)
		}
		if !hit {
			t.Errorf("%q: %v", body, problems)
		}
		if _, ok := l.Strings["x"]; ok && want != "unknown key" {
			t.Errorf("%q: a refused text loaded: %v", body, l.Strings["x"])
		}
	}
}
