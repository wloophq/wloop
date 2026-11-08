package web

import (
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var (
	rowT1 = time.Date(2026, 10, 12, 9, 30, 0, 0, time.UTC)
	rowT2 = time.Date(2026, 10, 11, 16, 45, 0, 0, time.UTC)
)

// rowJS renders tasks with the row and reads back its slots: each node's class, a facet button read as the
// piece it wraps.
const rowJS = `
Date.now = () => input.now;
tasksState.catalog = { ...input.cat, states: [{ name: "next", label: "Next", category: "ready" }] };
tasksState.catalog.facets = [...tasksState.catalog.facets, { id: "ask", label: "Ask", group: true, show: "kind" }];
const lens = lensOf({ view: "inbox", q: "", g: "", s: "", layout: "", area: "", ctx: [], tk: "", done: false });
const cls = (n) => (n.attrs.class === "facet" ? cls(n.children[0]) : String(n.attrs.class || n.tag));
const slot = (n, name) => all(n, (x) => String(x.attrs.class || "").split(" ").includes(name))[0];
const kids = (n) => (n ? n.children.filter((c) => c && c.attrs).map(cls) : []);
const metaTexts = (row) => slot(row, "meta").children.filter((c) => c && c.attrs).flatMap((c) => (String(c.attrs.class).split(" ").includes("pieces") ? c.children.filter((x) => x && x.attrs) : [c])).map(text);
function shape(row) {
  return { Children: kids(row), L1: kids(slot(row, "l1")), L2: kids(slot(row, "l2")), When: kids(slot(row, "when")),
    Title: text(slot(row, "t")), Date: text(slot(row, "age")), Meta: metaTexts(row), Tag: row.tag, Class: String(row.attrs.class || "") };
}
const rowOf = (e, opts) => rowView(taskModel(e, "", "", lens), lens, opts);
`

type rowShape struct {
	Children, L1, L2, When []string
	Title, Date, Tag       string
	Class                  string
	Meta                   []string
}

func rowTask(mod func(*api.TaskView)) api.TaskView {
	return task("e-1", "next", func(e *api.TaskView) {
		e.Category, e.Title = "ready", "acme/api#8: Retry flaky uploads"
		e.Units, e.UnitLabels = []unit.Unit{{Provider: unit.GitHub, Key: "acme/api#8"}}, []string{"acme/api#8"}
		e.Changed, e.Reason = true, "checks fail on the upload test"
		e.Facets = map[string][]string{"state": {"next"}, "ask": {"Review"}, "risk": {"high"}, "tag": {"a", "b", "c"}, "turn": {"me"}, "waits-on": {"dana"},
			"owed": {rowT1.Format(time.RFC3339)}, "moved": {rowT2.Format(time.RFC3339)}}
		if mod != nil {
			mod(e)
		}
	})
}

func rowRun(t *testing.T, main string, in map[string]any, out any) {
	t.Helper()
	in["cat"], in["now"] = servedViews(t), time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC).UnixMilli()
	nodeRun(t, viewsPrelude(t)+rowJS, nil, main, in, out)
}

func TestARowHasTwoLinesInTheirOrder(t *testing.T) {
	var got rowShape
	rowRun(t, `process.stdout.write(JSON.stringify(shape(rowOf(input.task))));`, map[string]any{"task": rowTask(nil)}, &got)
	if want := []string{"glyph", "l1", "meta", "l2", "r2"}; !slices.Equal(got.Children, want) {
		t.Errorf("the row's slots %q, want %q", got.Children, want)
	}
	if want := []string{"t", "ch"}; !slices.Equal(got.L1, want) {
		t.Errorf("line 1 holds %q, want the title leading it, %q", got.L1, want)
	}
	if len(got.Meta) == 0 || got.Meta[0] != "Review" {
		t.Errorf("the meta pieces %q, want the kind first among them", got.Meta)
	}
	if want := []string{"ref", "why"}; !slices.Equal(got.L2, want) {
		t.Errorf("line 2 holds %q, want %q", got.L2, want)
	}
	if want := []string{"dot", "who", "age"}; !slices.Equal(got.When, want) {
		t.Errorf("line 2's right side holds %q, want %q", got.When, want)
	}
	if got.Title != "Retry flaky uploads" {
		t.Errorf("the title reads %q, want it without the ref line 2 shows", got.Title)
	}
	if !slices.Contains(got.Meta, "risk high") {
		t.Errorf("the meta pieces %q lack the risk", got.Meta)
	}
}

func TestARowsGlyphIsItsStateAndFiltersByIt(t *testing.T) {
	var got struct {
		Category, Title, Field, Value string
		Button                        bool
		Kinds                         []string
	}
	rowRun(t, `const row = rowOf(input.task);
const g = slot(row, "glyph");
const button = row.children[0];
process.stdout.write(JSON.stringify({ Category: g.attrs["data-category"], Title: g.attrs.title, Field: g.attrs["data-field"] || "", Value: g.attrs["data-value"] || "",
  Button: button.tag === "button" && button.attrs.class === "facet", Kinds: all(slot(row, "meta"), (x) => String(x.attrs.class || "").includes("verb")).map(text) }));`,
		map[string]any{"task": rowTask(nil)}, &got)
	if got.Category != "ready" || got.Title != "Next" || got.Field != "state" || got.Value != "next" || !got.Button {
		t.Errorf("the glyph %+v, want the ready shape titled Next, a button filtering state:next", got)
	}
	if slices.Contains(got.Kinds, "Next") {
		t.Errorf("the kind pieces repeat the state its glyph draws: %q", got.Kinds)
	}
}

func TestARowsDateFollowsTheTimeRule(t *testing.T) {
	var got struct{ Mine, Theirs, WantMine, WantTheirs string }
	theirs := rowTask(func(e *api.TaskView) { e.Facets["turn"] = []string{"others"} })
	rowRun(t, `process.stdout.write(JSON.stringify({ Mine: shape(rowOf(input.mine)).Date, Theirs: shape(rowOf(input.theirs)).Date,
  WantMine: "since " + fmtDate(input.t1, "datetime"), WantTheirs: "moved " + fmtDate(input.t2, "datetime") }));`,
		map[string]any{"mine": rowTask(nil), "theirs": theirs, "t1": rowT1.Format(time.RFC3339), "t2": rowT2.Format(time.RFC3339)}, &got)
	if got.Mine != got.WantMine || strings.Contains(got.Mine, "{{") {
		t.Errorf("a task on my turn dates %q, want %q", got.Mine, got.WantMine)
	}
	if got.Theirs != got.WantTheirs {
		t.Errorf("a task on another's turn dates %q, want %q", got.Theirs, got.WantTheirs)
	}
}

func TestARowShowsTwoTagsAndTheRest(t *testing.T) {
	var got struct {
		rowShape
		More string
	}
	rowRun(t, `tasksState.catalog.facets = tasksState.catalog.facets.map((f) => (f.id === "tag" ? { ...f, labels: { c: "Cê" } } : f));
const worded = lensOf({ view: "inbox", q: "", g: "", s: "", layout: "", area: "", ctx: [], tk: "", done: false });
const row = rowView(taskModel(input.task, "", "", worded), worded);
process.stdout.write(JSON.stringify({ ...shape(row), More: (all(row, (x) => "data-more" in x.attrs)[0] || { attrs: {} }).attrs.title }));`, map[string]any{"task": rowTask(nil)}, &got)
	if n := len(got.Meta); n < 3 || !slices.Equal(got.Meta[n-3:], []string{"a", "b", "+1"}) {
		t.Errorf("the meta pieces %q, want them to end a, b, +1", got.Meta)
	}
	if got.More != "Cê" {
		t.Errorf("the +1 chip's tooltip %q does not name the tag it holds", got.More)
	}
}

func TestARowWithPendingDraftsShowsTheMarker(t *testing.T) {
	var got struct {
		With  []string
		Title string
		None  int
	}
	with := rowTask(func(e *api.TaskView) { e.Drafts = []int64{7} })
	rowRun(t, `const marks = (row) => all(row, (x) => x.attrs.class === "draft-mark");
const w = marks(rowOf(input.with));
process.stdout.write(JSON.stringify({ With: w.map(text), Title: w.length ? w[0].attrs.title : "", None: marks(rowOf(input.none)).length }));`,
		map[string]any{"with": with, "none": rowTask(nil)}, &got)
	if len(got.With) != 1 || got.Title != "1 draft waits for you" {
		t.Errorf("the draft marker %q titled %q, want one titled \"1 draft waits for you\"", got.With, got.Title)
	}
	if got.None != 0 {
		t.Errorf("a task with no draft shows %d markers", got.None)
	}
}

func TestTheBoardCardUsesTheRowsSlots(t *testing.T) {
	var got struct{ Row, Card rowShape }
	rowRun(t, `process.stdout.write(JSON.stringify({ Row: shape(rowOf(input.task)), Card: shape(rowOf(input.task, { card: true })) }));`,
		map[string]any{"task": rowTask(nil)}, &got)
	if got.Card.Tag != "li" || !slices.Contains(strings.Fields(got.Card.Class), "card") {
		t.Errorf("the card is %s.%s, want li.card", got.Card.Tag, got.Card.Class)
	}
	for _, s := range []string{"open", "ch"} {
		if !slices.Contains(got.Card.L1, s) {
			t.Errorf("the card's line 1 %q lacks %s", got.Card.L1, s)
		}
	}
	if got.Card.Title != got.Row.Title || !slices.Equal(got.Card.When, got.Row.When) || !slices.Equal(got.Card.Meta, got.Row.Meta) {
		t.Errorf("the card %+v and its row %+v show different slots", got.Card, got.Row)
	}
}

// TestALongRowKeepsWhoAndItsDate renders a task with a 200-character title, a long reason and 20 tags: line 1
// holds two tags and the count of the rest, and line 2 still holds whom it waits on and the date.
func TestALongRowKeepsWhoAndItsDate(t *testing.T) {
	var got rowShape
	long := rowTask(func(e *api.TaskView) {
		e.Title = strings.Repeat("Retry the flaky uploads ", 9)[:200]
		e.Reason = strings.Repeat("the upload test fails when the bucket is slow ", 10)
		var tags []string
		for i := range 20 {
			tags = append(tags, "t"+string(rune('a'+i)))
		}
		e.Facets["tag"] = tags
	})
	rowRun(t, `process.stdout.write(JSON.stringify(shape(rowOf(input.task))));`, map[string]any{"task": long}, &got)
	tags := 0
	for _, m := range got.Meta {
		if len(m) == 2 && m[0] == 't' {
			tags++
		}
	}
	if tags != 2 || got.Meta[len(got.Meta)-1] != "+18" {
		t.Errorf("line 1's meta %q, want two tags then +18", got.Meta)
	}
	if !slices.Contains(got.When, "who") || !slices.Contains(got.When, "age") || got.Date == "" {
		t.Errorf("line 2's right side %q dated %q, want who and the date", got.When, got.Date)
	}
}

// cssRule is the body of the rule of app.css whose selector is sel, alone on its line.
func cssRule(t *testing.T, css, sel string) string {
	t.Helper()
	m := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(sel) + `\s*\{([^}]*)\}`).FindStringSubmatch(css)
	if m == nil {
		t.Fatalf("app.css has no rule %s", sel)
	}
	return m[1]
}

func TestRowCSSKeepsEachLineOnOneLine(t *testing.T) {
	raw, err := fs.ReadFile(files, "static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(raw)
	for _, sel := range []string{".row .t", ".row .why"} {
		body := cssRule(t, css, sel)
		for _, want := range []string{"white-space: nowrap", "text-overflow: ellipsis", "overflow: hidden", "min-width: 0"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s lacks %s: %s", sel, want, body)
			}
		}
	}
	row := cssRule(t, css, ".row")
	for _, want := range []string{"min-height: var(--row-height)", "grid-template-columns: 16px minmax(min(40%, 30rem), 1fr) minmax(0, auto)"} {
		if !strings.Contains(row, want) {
			t.Errorf(".row lacks %s: %s", want, row)
		}
	}
	for _, sel := range []string{".row .l1", ".row .l2"} {
		if !strings.Contains(cssRule(t, css, sel), "min-width: 0") {
			t.Errorf("%s does not shrink, so it could push whom the row waits on off the row", sel)
		}
	}
	if !strings.Contains(cssRule(t, css, ".row .r2"), "white-space: nowrap") {
		t.Error(".row .r2 wraps, so who and the date could break across lines")
	}
}

func TestARowMarksTheOpenTaskAsCurrent(t *testing.T) {
	var got []map[string]string
	rowRun(t, `
const marks = (r) => ({ current: String(r.attrs["aria-current"] ?? ""), selected: String(r.attrs["aria-selected"] ?? "") });
process.stdout.write(JSON.stringify([marks(rowView(taskModel(input.task, "e-1", "", lens), lens)), marks(rowView(taskModel(input.task, "e-2", "", lens), lens))]));`,
		map[string]any{"task": rowTask(nil)}, &got)
	want := []map[string]string{{"current": "true", "selected": ""}, {"current": "", "selected": ""}}
	if mustJS(t, got) != mustJS(t, want) {
		t.Errorf("the open row and another mark %v, want %v", got, want)
	}
	page := staticJS(t, "index.html")
	if !strings.Contains(page, `<div id="table"></div>`) || strings.Contains(page, "<table") {
		t.Error("index.html holds the row list in a table, which reads as a table without rows")
	}
}

func TestTheRowsArrowsSkipWhatItHides(t *testing.T) {
	var got []string
	nodeRun(t, "", []string{"rowStops"}, `
const el = (name, kids = [], extra = {}) => ({ name, children: kids, hidden: false, ...extra,
  getAttribute: (k) => (k === "data-rove" ? "" : null) });
const shown = el("Review"), hidden = el("Remover", [], { inert: true });
const row = el("row", [el("l1", [el("title")]), el("meta", [el("acts", [shown, hidden])])]);
process.stdout.write(JSON.stringify(rowStops(row).map((n) => n.name)));`, nil, &got)
	if slices.Contains(got, "Remover") || !slices.Contains(got, "Review") {
		t.Fatalf("stops %v: the row's arrows skip an action that wrapped out of sight", got)
	}
}
