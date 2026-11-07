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

func TestTheBundledStringsLoadInPtBR(t *testing.T) {
	l, problems := LoadIn("pt-BR", bundledOnly(), bundledDefault(t), testBase(), nil)
	for _, p := range problems {
		if strings.Contains(p.File, StringsFile) {
			t.Errorf("%v", p)
		}
	}
	if l.Strings["Sweep now"]["other"] != "Varrer agora" || l.Strings["Quit {name}"]["other"] != "Sair do {name}" || l.Formats["time"] != "15:04" {
		t.Fatalf("%v %v", l.Strings["Sweep now"], l.Formats)
	}
}

func TestATranslationGivesTheFormsItsLocaleNeeds(t *testing.T) {
	bundled := bundledWith(t, map[string]string{
		StringsFile:                  "[strings]\n\"{n} drafts\" = { one = \"{n} draft\", other = \"{n} drafts\" }\n\"{n} open\" = \"{n} open\"\n\"{n} left\" = \"{n} left\"\n",
		"locales/pt-BR/strings.toml": "[strings]\n\"{n} drafts\" = { one = \"{n} rascunho\", many = \"{n} de rascunhos\", other = \"{n} rascunhos\" }\n\"{n} open\" = { one = \"{n} aberto\", other = \"{n} abertos\" }\n\"{n} left\" = { one = \"{n} sobrou\", single = \"x\", other = \"{n} sobraram {all}\" }\n",
	})
	l, problems := LoadIn("pt-BR", bundledOnly(), bundled, testBase(), nil)
	if d := l.Strings["{n} drafts"]; d["one"] != "{n} rascunho" || d["many"] != "{n} de rascunhos" || d["other"] != "{n} rascunhos" {
		t.Errorf("drafts: %v", d)
	}
	if o := l.Strings["{n} open"]; o["one"] != "{n} aberto" || o["other"] != "{n} abertos" {
		t.Errorf("open: %v", o)
	}
	if left := l.Strings["{n} left"]; left["other"] != "{n} left" || len(left) != 1 {
		t.Errorf("a refused translation shows English: %v", left)
	}
	var single, all bool
	for _, p := range problems {
		single = single || p.Field == "strings.{n} left.single" && strings.Contains(p.Msg, "no plural form")
		all = all || p.Field == "strings.{n} left.other" && strings.Contains(p.Msg, "{all}")
	}
	if !single || !all {
		t.Errorf("%v", problems)
	}
	en, _ := LoadIn("en", bundledOnly(), bundled, testBase(), nil)
	if o := en.Strings["{n} open"]; o["other"] != "{n} open" || len(o) != 1 {
		t.Errorf("English: %v", o)
	}
}

func TestTheStringsStampFollowsLocaleAndWords(t *testing.T) {
	bundled := bundledWith(t, stringsFixture)
	en, _ := LoadIn("en", bundledOnly(), bundled, testBase(), nil)
	again, _ := LoadIn("en", bundledOnly(), bundled, testBase(), nil)
	pt, _ := LoadIn("pt-BR", bundledOnly(), bundled, testBase(), nil)
	dir := userDir(t, map[string]string{StringsFile: "[strings]\n\"Open cockpit\" = \"Show the cockpit\"\n"})
	reworded, _ := LoadIn("en", userLayer(dir), bundled, testBase(), nil)
	if en.StringsStamp == "" || en.StringsStamp != again.StringsStamp || en.StringsStamp == pt.StringsStamp || en.StringsStamp == reworded.StringsStamp {
		t.Fatalf("%q %q %q %q", en.StringsStamp, again.StringsStamp, pt.StringsStamp, reworded.StringsStamp)
	}
}
