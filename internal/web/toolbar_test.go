package web

import (
	"slices"
	"strings"
	"testing"
)

// mineRows are lensRows listed by My PRs.
func mineRows() []map[string]any {
	rows := wireRows("me", tasklensNow, lensRows()...)
	for _, r := range rows {
		r["views"] = []string{"mine"}
	}
	return rows
}

// toolbarPrelude is mixedPrelude over mineRows with the row view's pipeline, recording the hash changes it
// writes.
func toolbarPrelude(t *testing.T) string {
	var b strings.Builder
	b.WriteString(mixedPrelude(t))
	for _, f := range []string{"pipeline", "byDefault", "sinceTime", "isSet"} {
		b.WriteString(jsFunction(t, cockpitJS(t), f))
	}
	b.WriteString(toolbarState)
	return b.String()
}

const toolbarState = `
state.rows = input.rows;
state.loaded = true;
const written = [];
writeHash = (c) => { written.push(c); };
`

func TestTheToolbarShowsEachFilterAsARemovableChip(t *testing.T) {
	var got struct {
		Chips, Labels []string
		Written       []map[string]string
	}
	nodeRun(t, toolbarPrelude(t), nil, `
const lens = lensOf(route({ view: "mine", q: "wait:me risk:high" }));
const chips = byClass(lensToolbar(lens, pipeline(state.rows, lens)), "chip");
chips.find((c) => text(c).startsWith("Risk")).attrs.onclick();
process.stdout.write(JSON.stringify({ Chips: chips.map(text), Labels: chips.map((c) => c.attrs["aria-label"]), Written: written }));`,
		mixedInput(t, map[string]any{"rows": mineRows()}), &got)
	if want := []string{"Waits on (role): me ×", "Risk: high ×"}; !slices.Equal(got.Chips, want) {
		t.Errorf("chips %q, want %q", got.Chips, want)
	}
	if want := []string{"Remove filter Waits on (role): me", "Remove filter Risk: high"}; !slices.Equal(got.Labels, want) {
		t.Errorf("chip labels %q, want %q", got.Labels, want)
	}
	if len(got.Written) != 1 || got.Written[0]["q"] != "wait:me" {
		t.Errorf("removing the risk chip writes %v, want q wait:me", got.Written)
	}
}

// oldColumnSortJS is the field each column head of the table sorted by before the two-line row: the first
// sortable facet a column's parts show, the strip's for the who column; columns no row fills had no head.
const oldColumnSortJS = `
const OLD_COLUMNS = [["strip", "who", "kind"], ["age"], ["aside"], ["ref"]];
function oldColumnSorts(lens, items) {
  const filled = (f) => f && items.some((it) => facetValues(it, f.id).length);
  const out = new Set();
  for (const parts of OLD_COLUMNS) {
    for (const part of parts) {
      if (part === "strip") {
        const f = lens.strip ? fieldById(lens.strip, lens.fields) : null;
        if (f && f.sort && filled(f)) { out.add(f.id); break; }
        continue;
      }
      const s = (lens.specs || []).find((x) => { const f = fieldById(x.id, lens.fields); return f && f.sort && filled(f) && specFor(x, lens.provider || "").show === part; });
      if (s) { out.add(s.id); break; }
    }
  }
  return [...out];
}
`

func TestTheSortChipOffersTheOldColumnSorts(t *testing.T) {
	var got map[string]struct{ Old, Options []string }
	nodeRun(t, toolbarPrelude(t)+oldColumnSortJS, nil, `
const out = {};
for (const view of ["mine", "needs-me"]) {
  const lens = lensOf(route({ view }));
  const bar = lensToolbar(lens, lensResult(lens));
  out[view] = { Old: oldColumnSorts(lens, lensItems(lens)), Options: options(bar, "lens:sort") };
}
process.stdout.write(JSON.stringify(out));`, mixedInput(t, map[string]any{"rows": mineRows()}), &got)
	for view, g := range got {
		if len(g.Old) == 0 {
			t.Errorf("%s: no column sorted before; the fixture proves nothing", view)
		}
		if len(g.Options) == 0 || g.Options[0] != "" {
			t.Errorf("%s: the Sort chip %q does not start with the view's own order", view, g.Options)
		}
		for _, id := range g.Old {
			if !slices.Contains(g.Options, id) {
				t.Errorf("%s: the Sort chip %q lacks %s, which a column head sorted by", view, g.Options, id)
			}
		}
	}
}

func TestTheFilterHintIsTheServedKey(t *testing.T) {
	var got struct{ Kbd, Shortcut, Before string }
	keysRun(t, toolbarPrelude(t), `
const pill = () => all(lensToolbar(lensOf(route({ view: "mine" })), null), (n) => n.attrs && n.attrs["data-focus"] === "lens:add-filter")[0];
const before = all(pill(), (n) => n.tag === "kbd").length + (pill().attrs["aria-keyshortcuts"] || "");
cockpitKeys = createKeys({});
cockpitKeys.setBindings(BINDINGS.map((b) => (b.action === "filter:add" ? { ...b, key: "=" } : b)));
const p = pill();
return { Kbd: all(p, (n) => n.tag === "kbd").map(text).join("|"), Shortcut: p.attrs["aria-keyshortcuts"] || "", Before: String(before) };`,
		mixedInput(t, map[string]any{"rows": mineRows(), "task": taskFixture()}), &got)
	if got.Kbd != "=" || got.Shortcut != "=" {
		t.Errorf("the Filter pill hints %q with shortcut %q, want the served key of filter:add", got.Kbd, got.Shortcut)
	}
	if got.Before != "0" {
		t.Errorf("before the keys load the pill hints %q, want no hint", got.Before)
	}
}

func TestTheToolbarRedrawsWhenTheKeysLoad(t *testing.T) {
	var got struct{ Before, After string }
	keysRun(t, toolbarPrelude(t), `
const tools = { dataset: {}, hidden: false, kids: [], replaceChildren(...c) { this.kids = c; } };
const lens = lensOf(route({ view: "mine" }));
const kbd = () => all(tools.kids, (n) => n.attrs && n.attrs["data-focus"] === "lens:add-filter").flatMap((p) => all(p, (n) => n.tag === "kbd")).map(text).join("|");
renderLens(lens, null, tools);
const before = kbd();
cockpitKeys = createKeys({});
cockpitKeys.setBindings(BINDINGS);
renderLens(lens, null, tools);
return { Before: before, After: kbd() };`,
		mixedInput(t, map[string]any{"rows": mineRows(), "task": taskFixture()}), &got)
	if got.Before != "" || got.After != "+" {
		t.Errorf("the Filter pill hints %q before the keys load and %q after, want none then +", got.Before, got.After)
	}
}

func TestFilterKeyOpensTheFieldPalette(t *testing.T) {
	var plus []string
	for _, b := range webBindings(t) {
		if b.Key == "+" {
			plus = append(plus, b.Context+" "+b.Action)
		}
		if b.Key == "f" && b.Context == "list" && b.Action != "focus" {
			t.Errorf("f runs %s in the list, want focus", b.Action)
		}
	}
	if !slices.Equal(plus, []string{"list filter:add"}) {
		t.Fatalf("the + key is bound to %v, want only filter:add in the list", plus)
	}
	var got struct {
		Title, ValueTitle string
		Fields, Offered   []string
		Values            []string
		Want              string
		Written           []map[string]string
		Palettes          int
		Posts             []string
	}
	keysRun(t, toolbarPrelude(t), `
const lens = lensOf(route({ view: "mine" }));
const items = lensItems(lens);
const palettes = [];
const hz = harness({ selected: input.task, addFilter: () => filterPalette(lens, items, (p) => { palettes.push(p); }) });
await hz.press("+", { shift: true });
const fields = palettes[0];
const risk = filterFields(lens, items).find((f) => f.id === "risk");
fields.items.find((it) => it.label === risk.label).run();
const values = palettes[1];
values.items[0].run();
await hz.press("f");
return { Title: fields.title, Fields: fields.items.map((it) => it.label), Offered: filterFields(lens, items).map((f) => f.label),
  ValueTitle: values.title, Values: values.items.map((it) => it.label), Want: "risk:" + filterValues(risk, items)[0].value,
  Written: written, Palettes: palettes.length, Posts: hz.log.posts.map((p) => p.url) };`,
		mixedInput(t, map[string]any{"rows": mineRows(), "task": taskFixture()}), &got)
	if got.Title != "Filter" || len(got.Fields) < 2 || !slices.Equal(got.Fields, got.Offered) {
		t.Errorf("+ opens %q over %q, want Filter over the lens fields %q", got.Title, got.Fields, got.Offered)
	}
	if got.ValueTitle != "Risk" || !slices.Equal(got.Values, []string{"high", "medium", "low", "no brief"}) {
		t.Errorf("picking Risk opens %q over %q", got.ValueTitle, got.Values)
	}
	if len(got.Written) != 1 || got.Written[0]["q"] != got.Want {
		t.Errorf("picking a value writes %v, want q %s", got.Written, got.Want)
	}
	if got.Palettes != 2 || len(got.Posts) != 1 || !strings.HasSuffix(got.Posts[0], "/focus") {
		t.Errorf("f after the palette: %d palettes, posts %v; want f to keep toggling Focus", got.Palettes, got.Posts)
	}
}
