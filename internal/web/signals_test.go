package web

import (
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/card"
	intentload "github.com/wloophq/wloop/internal/intent"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/internal/profile"
	"github.com/wloophq/wloop/internal/turn"
)

// signalsCatalog is the catalog of the bundled GitHub plugin's card signals in locale's words.
func signalsCatalog(t *testing.T, locale string) api.CatalogResponse {
	t.Helper()
	base, err := turn.Base()
	if err != nil {
		t.Fatal(err)
	}
	l, problems := intentload.LoadIn(locale, profile.BundledChain(), turn.BundledFS(), base, turn.CheckTemplate)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	c := psource.BundledCatalog("github")
	c.Texts = func() intentload.UITexts { return l.UI["github"] }
	return api.CatalogResponse{CardSignals: c.CardSignals()}
}

// signalCards are a stalled pull request with changes asked, failing checks and conflicts, one approved and
// green, and one whose checks are pending with a signal no words name.
func signalCards() []api.CardView {
	stalled := []card.Note{{Code: "stalled", Tone: "warn", Params: map[string]string{"days": "3"}}}
	return []api.CardView{
		{Card: card.Card{Unit: prUnit, Label: "acme/api#8", Signals: []card.Signal{
			{Key: "situation", Value: "changes", Tone: "neutral", Notes: stalled},
			{Key: "checks", Value: "failing_named", Tone: "bad", Params: map[string]string{"names": "lint, test"}},
			{Key: "review", Value: "changes_requested", Tone: "warn"},
			{Key: "merge", Value: "conflicts", Tone: "bad"},
		}}},
		{Card: card.Card{Unit: prUnit, Label: "acme/api#9", Signals: []card.Signal{
			{Key: "checks", Value: "passing", Tone: "good"},
			{Key: "review", Value: "approved", Tone: "good"},
		}}},
		{Card: card.Card{Unit: prUnit, Label: "acme/api#10", Signals: []card.Signal{
			{Key: "checks", Value: "pending", Tone: "warn"},
			{Key: "lane", Value: "fast_track", Tone: "info", Notes: []card.Note{{Code: "odd", Tone: "warn"}}},
		}}},
	}
}

// signalsShownJS reads back the props grid's signal rows and each unit card's signal lines, every one as its
// label, its text and the tones of its value and its notes.
const signalsShownJS = `
applyCatalog(input.answers.catalog);
const e = input.answers.task;
const props = taskProps(e);
const dts = walk(props, (x) => x.tag === "dt"), dds = walk(props, (x) => x.tag === "dd");
const toneOf = (n) => (n.attrs.class || "").split(" ").find((c) => c.startsWith("tone-")) || "";
const notesOf = (n) => walk(n, (x) => x !== n && x.attrs && (x.attrs.class || "").startsWith("tone-")).map((x) => toneOf(x) + ":" + x.textContent);
const grid = dts.map((d, i) => [d.textContent, dds[i].textContent, toneOf(dds[i]), ...notesOf(dds[i])]).filter((r) => r[2]);
const cards = e.cards.map((c) => walk(unitCard(c), (x) => x.tag === "li").map((li) => {
  const k = walk(li, (x) => x.attrs && x.attrs.class === "k")[0];
  return [k.textContent, li.textContent.slice(k.textContent.length + 1), toneOf(li), ...notesOf(li)];
}));
done({ Grid: grid, Cards: cards });`

type signalsShown struct {
	Grid  [][]string
	Cards [][][]string
}

func TestCardSignalsReadInTheCatalogsWords(t *testing.T) {
	for _, c := range []struct {
		locale string
		want   [][]string
	}{
		{"en", [][]string{
			{"situation", "changes asked · stalled for 3 working days", "tone-neutral", "tone-warn:stalled for 3 working days"},
			{"checks", "failing: lint, test", "tone-bad"},
			{"review", "changes requested", "tone-warn"},
			{"merge", "conflicts", "tone-bad"},
			{"checks", "passing", "tone-good"},
			{"review", "approved", "tone-good"},
			{"checks", "pending", "tone-warn"},
			{"lane", "fast_track · odd", "tone-info", "tone-warn:odd"},
		}},
		{"pt-BR", [][]string{
			{"situação", "mudanças pedidas · parado há 3 dias úteis", "tone-neutral", "tone-warn:parado há 3 dias úteis"},
			{"checks", "falhando: lint, test", "tone-bad"},
			{"review", "mudanças pedidas", "tone-warn"},
			{"merge", "conflitos", "tone-bad"},
			{"checks", "passando", "tone-good"},
			{"review", "aprovada", "tone-good"},
			{"checks", "pendentes", "tone-warn"},
			{"lane", "fast_track · odd", "tone-info", "tone-warn:odd"},
		}},
	} {
		t.Run(c.locale, func(t *testing.T) {
			e := pageTask(t)
			e.Cards = signalCards()
			var got signalsShown
			runTaskPageOn(t, taskCatalog(), `I18N.locale = "`+c.locale+`";`+signalsShownJS,
				map[string]any{"task": e, "catalog": signalsCatalog(t, c.locale)}, &got)
			if mustJS(t, got.Grid) != mustJS(t, c.want) {
				t.Errorf("props grid\n got %v\nwant %v", got.Grid, c.want)
			}
			var lines [][]string
			for _, card := range got.Cards {
				lines = append(lines, card...)
			}
			if mustJS(t, lines) != mustJS(t, c.want) {
				t.Errorf("unit cards\n got %v\nwant %v", lines, c.want)
			}
		})
	}
}

func TestACardSignalWithNoCatalogShowsItsCodes(t *testing.T) {
	e := pageTask(t)
	e.Cards = signalCards()[:1]
	var got signalsShown
	runTaskPageOn(t, taskCatalog(), signalsShownJS, map[string]any{"task": e, "catalog": api.CatalogResponse{}}, &got)
	want := [][]string{
		{"situation", "changes · stalled", "tone-neutral", "tone-warn:stalled"},
		{"checks", "failing_named", "tone-bad"},
		{"review", "changes_requested", "tone-warn"},
		{"merge", "conflicts", "tone-bad"},
	}
	if mustJS(t, got.Grid) != mustJS(t, want) {
		t.Errorf("props grid\n got %v\nwant %v", got.Grid, want)
	}
}
