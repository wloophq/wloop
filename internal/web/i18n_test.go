package web

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/wloophq/wloop/internal/i18n"
)

func TestTFallsBackToTheKey(t *testing.T) {
	var got struct{ Plain, Params, Missing, Plural string }
	nodeRun(t, staticJS(t, "i18n.js"), nil, `
process.stdout.write(JSON.stringify({
  Plain: t("Open cockpit"),
  Params: t("{n} of {total}", { n: 2, total: 5 }),
  Missing: t("{n} of {total}", { n: 2 }),
  Plural: tn("{n} drafts", 3),
}));`, nil, &got)
	if got.Plain != "Open cockpit" || got.Params != "2 of 5" || got.Missing != "2 of {total}" || got.Plural != "3 drafts" {
		t.Fatalf("got %+v", got)
	}
}

func TestTAndTnInPtBR(t *testing.T) {
	var got struct{ Open, Zero, One, Many, ThroughT, Unknown, OnlyOther string }
	nodeRun(t, staticJS(t, "i18n.js"), nil, `
I18N.locale = "pt-BR";
I18N.strings = {"Open cockpit": "Abrir o cockpit", "{n} drafts": {one: "{n} rascunho", other: "{n} rascunhos"}, "{n} items": {other: "{n} itens"}};
process.stdout.write(JSON.stringify({
  Open: t("Open cockpit"), Zero: tn("{n} drafts", 0), One: tn("{n} drafts", 1), Many: tn("{n} drafts", 1234),
  ThroughT: t("{n} drafts"), Unknown: tn("{n} tasks", 2), OnlyOther: tn("{n} items", 1),
}));`, nil, &got)
	if got.Open != "Abrir o cockpit" || got.Zero != "0 rascunho" || got.One != "1 rascunho" || got.Many != "1.234 rascunhos" ||
		got.ThroughT != "{n} rascunhos" || got.Unknown != "2 tasks" || got.OnlyOther != "1 itens" {
		t.Fatalf("got %+v", got)
	}
}

func TestFmtDateAndNumberFollowTheLocale(t *testing.T) {
	var got struct{ PtDate, PtTime, PtDateTime, PtLong, PtNum, EnDate, EnTime, EnNum, Bad string }
	nodeScriptEnv(t, []string{"TZ=UTC"}, staticJS(t, "i18n.js")+`
const iso = "2026-11-07T21:05:00Z";
const out = {};
I18N.locale = "pt-BR";
Object.assign(out, {PtDate: fmtDate(iso, "date"), PtTime: fmtDate(iso, "time"), PtDateTime: fmtDate(iso, "datetime"), PtLong: fmtDate(iso, "long"), PtNum: fmtNumber(1234.5), Bad: fmtDate("bad", "date")});
I18N.locale = "en-US";
Object.assign(out, {EnDate: fmtDate(iso, "date"), EnTime: fmtDate(iso, "time"), EnNum: fmtNumber(1234.5)});
process.stdout.write(JSON.stringify(out));`, nil, &got)
	if got.PtDate != "07/11" || got.PtTime != "21:05" || !contains(got.PtDateTime, "07/11", "21:05") || !contains(got.PtLong, "2026", "nov") || got.PtNum != "1.234,5" ||
		got.EnDate != "11/07" || !contains(got.EnTime, "09:05", "PM") || got.EnNum != "1,234.5" || got.Bad != "" {
		t.Fatalf("got %+v", got)
	}
}

func contains(s string, parts ...string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}

func TestPluralParityWithGo(t *testing.T) {
	counts := []float64{0, 1, 2, 5, 1.5, 21, 101, 1000000}
	locales := []string{"en", "pt-BR", "fr", "ru", "ar"}
	var got map[string][]string
	nodeRun(t, staticJS(t, "i18n.js"), nil, `
const out = {};
for (const l of input.locales) {
  I18N.locale = l;
  I18N.strings = {k: {one: "one", other: "other"}};
  out[l] = input.counts.map((n) => [new Intl.PluralRules(l).select(n), tn("k", n)]).flat();
}
process.stdout.write(JSON.stringify(out));`, map[string]any{"locales": locales, "counts": counts}, &got)
	forms := i18n.Forms{"one": "one", "other": "other"}
	for _, l := range locales {
		for i, n := range counts {
			category, text := got[l][2*i], got[l][2*i+1]
			if want := forms.For(l, n); text != want {
				t.Errorf("%s %v: JS reads %q, Go %q", l, n, text, want)
			}
			// Intl's CLDR is newer than x/text's: it names millions "many" in pt and fr, which a text without
			// a many form reads as other in both.
			if want := i18n.Form(l, n); category != want && n < 1000000 {
				t.Errorf("%s %v: JS %q, Go %q", l, n, category, want)
			}
		}
	}
}

func TestBootRendersEnglishWhenStringsFail(t *testing.T) {
	var got struct {
		FailLocale, FailText  string
		SlowBefore, SlowAfter string
		Changed               int
		BadLocale             string
		InTimeLocale          string
		InTimeChanged         int
	}
	nodeRun(t, staticJS(t, "i18n.js"), nil, `
let changed = 0;
function i18nChanged() { changed++; }
(async () => {
  const out = {};
  await loadStrings(() => Promise.reject(new Error("HTTP 500")), 10);
  out.FailLocale = I18N.locale; out.FailText = t("Open cockpit");
  let release;
  const slow = new Promise((r) => { release = r; });
  await loadStrings(() => slow, 10);
  out.SlowBefore = t("Open cockpit");
  release({locale: "pt-BR", strings: {"Open cockpit": "Abrir o cockpit"}});
  await new Promise((r) => setTimeout(r, 20));
  out.SlowAfter = t("Open cockpit"); out.Changed = changed;
  await loadStrings(async () => ({locale: "not a locale!!", strings: {"Open cockpit": "x"}}), 10);
  out.BadLocale = I18N.locale;
  changed = 0;
  await loadStrings(async () => ({locale: "en", strings: {}}), 1000);
  out.InTimeLocale = I18N.locale; out.InTimeChanged = changed;
  process.stdout.write(JSON.stringify(out));
})();`, nil, &got)
	if got.FailLocale != "en" || got.FailText != "Open cockpit" || got.SlowBefore != "Open cockpit" || got.SlowAfter != "Abrir o cockpit" ||
		got.Changed != 1 || got.BadLocale != "pt-BR" || got.InTimeLocale != "en" || got.InTimeChanged != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestTheCockpitFollowsALiveLocaleChange(t *testing.T) {
	var got struct {
		Reads, Changed         int
		Before, After          string
		FailedBootReads        int
		FailedBootText         string
		RetryReads, StaleReads int
		RetryText, StaleText   string
	}
	nodeRun(t, staticJS(t, "i18n.js"), nil, `
let changed = 0, reads = 0, fail = false, answer = null;
function i18nChanged() { changed++; }
const pt = (stamp) => ({locale: "pt-BR", stamp, strings: {"Open cockpit": "Abrir o cockpit " + stamp}});
const getJSON = async () => { reads++; if (fail) throw new Error("HTTP 500"); return answer || pt("pt-2"); };
const tick = () => new Promise((r) => setTimeout(r, 10));
(async () => {
  const out = {};
  applyStrings({locale: "en", stamp: "en-1", strings: {}});
  followStrings("en-1", getJSON);
  followStrings("en-1", getJSON);
  out.Before = t("Open cockpit");
  followStrings("pt-2", getJSON);
  await tick();
  Object.assign(out, {Reads: reads, Changed: changed, After: t("Open cockpit")});

  I18N.locale = "en"; I18N.strings = {}; I18N.stamp = ""; reads = 0;
  followStrings("pt-2", getJSON);
  await tick();
  Object.assign(out, {FailedBootReads: reads, FailedBootText: t("Open cockpit")});

  reads = 0; fail = true;
  followStrings("pt-3", getJSON);
  await tick();
  fail = false; answer = pt("pt-3");
  followStrings("pt-3", getJSON);
  await tick();
  Object.assign(out, {RetryReads: reads, RetryText: t("Open cockpit")});

  reads = 0; answer = pt("pt-4");
  followStrings("pt-5", getJSON);
  await tick();
  Object.assign(out, {StaleReads: reads, StaleText: t("Open cockpit")});
  process.stdout.write(JSON.stringify(out));
})();`, nil, &got)
	if got.Reads != 1 || got.Changed != 1 || got.Before != "Open cockpit" || got.After != "Abrir o cockpit pt-2" {
		t.Errorf("a live change: %+v", got)
	}
	if got.FailedBootReads != 1 || got.FailedBootText != "Abrir o cockpit pt-2" {
		t.Errorf("after a failed boot read: %+v", got)
	}
	if got.RetryReads != 2 || got.RetryText != "Abrir o cockpit pt-3" {
		t.Errorf("a failed read is retried: %+v", got)
	}
	if got.StaleReads != 1 || got.StaleText != "Abrir o cockpit pt-3" {
		t.Errorf("an answer older than the latest health is dropped: %+v", got)
	}
}

// stringsPrelude sets I18N to locale and the [strings] table of the strings.toml at path.
func stringsPrelude(t *testing.T, locale, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Strings map[string]any `toml:"strings"`
	}
	if err := toml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	js, err := json.Marshal(doc.Strings)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("I18N.locale = %q;\nI18N.strings = %s;\n", locale, js)
}

func TestAppRendersPtBRChrome(t *testing.T) {
	prelude := filterPrelude(t) + stringsPrelude(t, "pt-BR", "../../profiles/default/locales/pt-BR/strings.toml") + `let activityLoopOnly = false;
const state = { loaded: true, rows: [], activityLoaded: true, activity: [{ event: { kind: "push" } }], explore: { busy: false, error: "", result: null } };
`
	var got []string
	nodeRun(t, prelude, append(slices.Clone(filterFuncs), "rowByKey", "emptyNote", "activityEmptyNote", "exploreStatus", "planWhen", "isSet", "age", "ago", "clearLens"), `
const flat = (x) => (Array.isArray(x) ? x.map(flat).join("") : x && typeof x === "object" ? Object.values(x).map(flat).join("") : String(x));
process.stdout.write(JSON.stringify([
  flat(emptyNote("mine", "")),
  flat(emptyNote("mine", "waits-on:bob zzz")),
  flat(emptyNote("mine", "https://github.com/acme/web/pull/3")),
  flat(activityEmptyNote("")),
  exploreStatus(),
  planWhen({ decision: "blocked" }),
  planWhen({ decision: "pending", default: "wait" }),
  ago(new Date(Date.now() - 5 * 60000).toISOString()),
]));`, nil, &got)
	want := []string{"Nada em My PRs agora.", "Nada em My PRs corresponde a \"waits-on:bob zzz\". Limpar filtros",
		"acme/web#3 não está no inventário. Explorar Abrir no GitHub", "Nenhum evento do loop na atividade recente.",
		"A busca só roda quando você aperta Enter ou Buscar; cada uma gasta cerca de 1 ponto da cota por hora do GitHub.",
		"bloqueado", "espera você", "há 5 min"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	text := strings.Join(got, "\n")
	for _, english := range []string{"Nothing in", "matches", "Clear filters", "is not in the inventory", "Explore it", "Open it on GitHub",
		"No loop events", "A search runs only", "blocked", "waits for you", "min ago"} {
		if strings.Contains(text, english) {
			t.Errorf("the pt-BR chrome still says %q: %q", english, text)
		}
	}
}
