package web

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
)

// cardJS reads back a rendered card and its list row: the card's element and open control, its facet
// buttons with whether each sits outside the open control and out of the Tab order, and the pieces both show.
const cardJS = `
function walk(n, f, out = [], inOpen = false) {
  if (!n || !n.attrs) return out;
  if (f(n)) out.push({ n, inOpen });
  for (const c of n.children || []) walk(c, f, out, inOpen || n.attrs.class === "open");
  return out;
}
const isFacet = (n) => n.tag === "button" && n.attrs.class === "facet";
const pieceOf = (n) => n.children[0].attrs["data-field"] + ":" + n.children[0].attrs["data-value"];
function cardShown(m, card, tr, aside) {
  const open = walk(card, (n) => n.attrs.class === "open")[0];
  const facets = walk(card, isFacet);
  const asidePieces = (aside || []).map((p) => p.field + ":" + p.value);
  return {
    Tag: card.tag, Class: card.attrs.class, Row: card.attrs["data-row"] || "", Tip: card.attrs.title || "", RowTip: tr.attrs.title || "",
    OpenTag: open ? open.n.tag : "", OpenFocus: open ? open.n.attrs["data-focus"] : "", OpenTab: open ? open.n.attrs.tabindex || "" : "none",
    OpenCurrent: open ? open.n.attrs["aria-current"] || "" : "", OpenHref: open ? open.n.attrs.href || "" : "",
    FacetsOutside: facets.every((x) => !x.inOpen), FacetsRove: facets.every((x) => x.n.attrs.tabindex === "-1" && "data-rove" in x.n.attrs),
    Pieces: facets.map((x) => pieceOf(x.n)),
    RowPieces: walk(tr, isFacet).map((x) => pieceOf(x.n)).filter((p) => !asidePieces.includes(p)),
  };
}
`

type cardShown struct {
	Tag, Class, Row, Tip, RowTip string
	OpenTag, OpenFocus, OpenTab  string
	OpenCurrent, OpenHref        string
	FacetsOutside, FacetsRove    bool
	Pieces, RowPieces            []string
}

// TestABoardCardIsItsRowsComponent renders a pull request, a Slack conversation, a task and a draft's task as
// board cards and as list rows: each card is an li whose title is the control that opens it, in the Tab order,
// its facet buttons are siblings of that control out of the Tab order, and it shows the pieces its row shows but
// the aside's, with the row's tooltip.
func TestABoardCardIsItsRowsComponent(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	pr := tagRows()[1]
	pr["views"] = []string{"mine"}
	pr["override"] = map[string]any{"hold": true}
	pr["workspace"] = map[string]any{"status": "reviewing"}
	slack := map[string]any{"unit": map[string]any{"provider": "slack", "key": slackUnit.Key}, "label": "#general", "title": "Deploy plan", "kind": "waits_on_others",
		"views": []string{"slack"}, "summary": map[string]any{"waits_on": "Ana", "since": "2026-10-12T09:00:00Z"},
		"facets": map[string][]string{"where": {"general"}, "waits-on": {"Ana"}, "turn": {"others"}, "situation": {"waits_on_others"}, "age": {"2026-10-12T09:00:00Z"}}}
	e := task("e-1", "next", func(e *api.TaskView) {
		e.Facets = map[string][]string{"state": {"next"}, "waits-on": {"me"}, "turn": {"me"}, "moved": {"2026-10-12T10:30:00Z"}}
	})
	draft := outboxTasks()[0]
	var got struct {
		PR, Slack, Task, Draft cardShown
		OpenedSlot, Hash       string
	}
	rowViewRun(t, bundledCatalog(t), cardJS+`state.rows = [input.pr, input.slack];
const pr = keyOf(input.pr);
function rowCardShown(r, view, sel) {
  const lens = lensOf({ ...readHash(), view });
  const m = rowModel(r, sel, lens, "", "card");
  return cardShown(m, rowCard(m, lens), rowTr(rowModel(r, sel, lens, ""), lens, rowColumnDefs()), m.parts.aside);
}
function taskCardShown(e, view) {
  const lens = lensOf({ ...readHash(), view });
  const m = taskModel(e, "", "", lens);
  return cardShown(m, rowCard(m, lens), rowTr(m, lens, rowColumnDefs()), m.parts.aside);
}
const out = { PR: rowCardShown(input.pr, "mine", pr), Slack: rowCardShown(input.slack, "slack", ""), Task: taskCardShown(input.task, "inbox"), Draft: taskCardShown(input.draft, "outbox") };
const lens = lensOf({ ...readHash(), view: "mine" });
walk(rowCard(rowModel(input.pr, "", lens, "", "card"), lens), (n) => n.attrs.class === "open")[0].n.on.click();
out.OpenedSlot = state.openSlot;
out.Hash = location.hash;
process.stdout.write(JSON.stringify(out));`,
		map[string]any{"pr": wireRow("me", now, pr), "slack": slack, "task": e, "draft": draft, "now": now.UnixMilli()}, &got)
	for name, c := range map[string]cardShown{"pull request": got.PR, "conversation": got.Slack, "task": got.Task, "draft": got.Draft} {
		if c.Tag != "li" || !slices.Contains(strings.Fields(c.Class), "card") || c.Row == "" || c.OpenFocus != c.Row || c.OpenTab != "" {
			t.Errorf("the %s's card %+v, want an li.card whose open control holds its focus slot in the Tab order", name, c)
		}
		if (len(c.Pieces) == 0 && name != "draft") || !c.FacetsOutside || !c.FacetsRove {
			t.Errorf("the %s's card facets %v: outside the open control %v, out of the Tab order %v", name, c.Pieces, c.FacetsOutside, c.FacetsRove)
		}
		if !slices.Equal(c.Pieces, c.RowPieces) {
			t.Errorf("the %s's card shows %v, its row %v", name, c.Pieces, c.RowPieces)
		}
		if c.Tip != c.RowTip {
			t.Errorf("the %s's card tooltip %q, its row's %q", name, c.Tip, c.RowTip)
		}
	}
	if got.PR.OpenTag != "button" || got.PR.OpenCurrent != "true" || got.Slack.OpenCurrent != "" || !strings.HasPrefix(got.PR.Row, "card:") {
		t.Errorf("the open pull request's card %+v and another's %+v", got.PR, got.Slack)
	}
	if got.Draft.OpenTag != "a" || !strings.Contains(got.Draft.OpenHref, "task=draft") {
		t.Errorf("a draft's card opens from its title link: %+v", got.Draft)
	}
	if !strings.HasPrefix(got.OpenedSlot, "card:") || !strings.Contains(got.Hash, "acme") {
		t.Errorf("clicking a card's title opened slot %q, hash %q", got.OpenedSlot, got.Hash)
	}
}

// TestTheArrowsMoveTheFocusInsideARow walks a list row and a card with the arrows, Home and End: the row, or the
// card's title, comes first and every other control after it in order, each out of the Tab order; a modified
// arrow, and a key on a control that is no stop, are left alone.
func TestTheArrowsMoveTheFocusInsideARow(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	pr := tagRows()[1]
	pr["views"] = []string{"mine"}
	pr["actions"] = []map[string]any{{"id": "open", "enabled": true}, {"id": "hold", "enabled": false}}
	var got struct {
		Stops, CardStops   []string
		Tabbable, CardTabs []string
		Moves              []string
		Ignored            []bool
	}
	rowViewRun(t, bundledCatalog(t), `state.rows = [input.pr];
state.specs = { version: "", byId: { open: { id: "open", label: "Open", surfaces: ["row"] }, hold: { id: "hold", label: "Pause", surfaces: ["row"] } } };
let focused = null;
El.prototype.focus = function () { focused = this; };
const name = (n) => n.attrs["data-focus"] || n.attrs.class || n.tag;
const lens = lensOf({ ...readHash(), view: "mine" });
const m = rowModel(input.pr, "", lens, "");
const tr = rowTr(m, lens, rowColumns([m]));
const card = rowCard(rowModel(input.pr, "", lens, "", "card"), lens);
const stops = rowStops(tr), cardStops = rowStops(card);
const tabbable = (root) => { const out = []; const w = (n) => { if (!n || !n.attrs) return; if (["button", "a", "tr"].includes(n.tag) && n.attrs.tabindex !== "-1" && !("disabled" in n.attrs)) out.push(name(n)); (n.children || []).forEach(w); }; w(root); return out; };
const press = (row, target, key, mods = {}) => { focused = null; const e = { key, currentTarget: row, target, preventDefault() {}, ...mods }; const took = roveKey(e); return { took, to: focused ? name(focused) : "" }; };
const moves = [press(tr, tr, "ArrowRight").to, press(tr, stops[1], "ArrowLeft").to, press(tr, tr, "End").to, press(tr, stops[stops.length - 1], "Home").to,
  press(card, cardStops[0], "ArrowRight").to];
const ignored = [press(tr, tr, "ArrowRight", { shiftKey: true }).took, press(tr, h("textarea", {}), "ArrowRight").took, press(tr, tr, "ArrowLeft").to === ""];
process.stdout.write(JSON.stringify({ Stops: stops.map(name), CardStops: cardStops.map(name), Tabbable: tabbable(tr), CardTabs: tabbable(card), Moves: moves, Ignored: ignored }));`,
		map[string]any{"pr": wireRow("me", now, pr), "now": now.UnixMilli()}, &got)
	if len(got.Stops) < 4 || !strings.HasPrefix(got.Stops[0], "row:") || !slices.ContainsFunc(got.Stops, func(s string) bool { return strings.HasSuffix(s, ":link") }) ||
		!slices.ContainsFunc(got.Stops, func(s string) bool { return strings.HasPrefix(s, "facet:") }) || !slices.ContainsFunc(got.Stops, func(s string) bool { return strings.HasSuffix(s, ":open") }) {
		t.Errorf("the row's stops %v, want the row, its facets, its link and its enabled actions", got.Stops)
	}
	if slices.ContainsFunc(got.Stops, func(s string) bool { return strings.HasSuffix(s, ":hold") }) {
		t.Errorf("a disabled action is a stop: %v", got.Stops)
	}
	if len(got.CardStops) < 2 || !strings.HasPrefix(got.CardStops[0], "card:") {
		t.Errorf("the card's stops %v, want its title first", got.CardStops)
	}
	if len(got.Tabbable) != 1 || got.Tabbable[0] != got.Stops[0] || len(got.CardTabs) != 1 || got.CardTabs[0] != got.CardStops[0] {
		t.Errorf("Tab reaches %v in the row and %v in the card, want the row and the card's title alone", got.Tabbable, got.CardTabs)
	}
	want := []string{got.Stops[1], got.Stops[0], got.Stops[len(got.Stops)-1], got.Stops[0], got.CardStops[1]}
	if !slices.Equal(got.Moves, want) {
		t.Errorf("moves %v, want %v", got.Moves, want)
	}
	if got.Ignored[0] || got.Ignored[1] || !got.Ignored[2] {
		t.Errorf("Shift+arrow taken %v, a key on no stop taken %v, Left on the first stop stays %v", got.Ignored[0], got.Ignored[1], got.Ignored[2])
	}
}

// TestACountdownAndATooltipSpeakTheRowWords reads a pending round's countdown and a row's tooltip in the
// profile's row words and in words a plugin replaces.
func TestACountdownAndATooltipSpeakTheRowWords(t *testing.T) {
	var got []string
	rowViewRun(t, bundledCatalog(t), `
catalog.lens = [{ provider: "slack", words: { countdown_seconds: "{{.s}}s to go", tip_kind: "{{.tip}} | {{.kind}}", tip_why: "{{.tip}} | {{.why}}", tip_named: "{{.strip}} [{{.who}}]" } }];
const at = (s) => new Date(input.now + s * 1000).toISOString();
const w = rowWords(""), slack = rowWords("slack");
const strip = { label: "Others' turn" };
process.stdout.write(JSON.stringify([left(at(5), w, input.now), left(at(125), w, input.now), left(at(-1), w, input.now), left(at(5), slack, input.now),
  rowTip(strip, "Ana", "waits on others", "she asked", w), rowTip(strip, "Ana", "waits on others", "she asked", slack), rowTip(null, "", "", "she asked", w)]));`,
		map[string]any{"now": time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC).UnixMilli()}, &got)
	want := []string{"in 5 s", "in 2m 05s", "sending", "5s to go", "Others' turn (Ana): waits on others. she asked", "Others' turn [Ana] | waits on others | she asked", "she asked"}
	if !slices.Equal(got, want) {
		t.Errorf("words %q, want %q", got, want)
	}
}
