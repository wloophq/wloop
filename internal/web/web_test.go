package web

import (
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestServesIndexAndAssets(t *testing.T) {
	h := Handler("")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `id="rail"`) || !strings.Contains(rec.Body.String(), `/static/app.js`) {
		t.Fatalf("%d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/static/app.js", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "EventSource") {
		t.Fatalf("%d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/nope", nil))
	if rec.Code != 404 {
		t.Fatalf("%d", rec.Code)
	}
}

func TestCockpitKnowsThePendingRound(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler("").ServeHTTP(rec, httptest.NewRequest("GET", "/static/app.js", nil))
	body := rec.Body.String()
	for _, want := range []string{"/api/pause", "data-deadline"} {
		if !strings.Contains(body, want) {
			t.Errorf("app.js lacks %s", want)
		}
	}
}

func TestCockpitHasTheRecentView(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler("").ServeHTTP(rec, httptest.NewRequest("GET", "/static/app.js", nil))
	body := rec.Body.String()
	for _, want := range []string{`v.id === "recent"`, "/api/activity", "renderActivity"} {
		if !strings.Contains(body, want) {
			t.Errorf("app.js lacks %s", want)
		}
	}
}

func TestCockpitHasTheDoneView(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler("").ServeHTTP(rec, httptest.NewRequest("GET", "/static/app.js", nil))
	body := rec.Body.String()
	for _, want := range []string{"/api/workspaces/remove-done", `removed: "workspace removed"`, "Remove all clean", `h("details"`, "out.detail", "k.detail"} {
		if !strings.Contains(body, want) {
			t.Errorf("app.js lacks %s", want)
		}
	}
}

// TestCockpitLabelsABlockedFirstRound: the GitHub plugin's layer words its
// round events as the loop's, and the cockpit shows the served words.
func TestCockpitLabelsABlockedFirstRound(t *testing.T) {
	raw, err := os.ReadFile("../../plugins/github/profile/ui.toml")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"[events.round_blocked]\nlabel = \"round blocked\"\nloop = true", "[events.round_unblocked]\nlabel = \"round unblocked\"\nloop = true"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("the GitHub layer lacks %q", want)
		}
	}
	var got struct {
		Label string `json:"label"`
		Loop  bool   `json:"loop"`
	}
	nodeRun(t, jsBlock(t, appJS(t), "EVENT_LABEL")+jsConst(t, appJS(t), "LOOP_EVENTS")+jsConst(t, appJS(t), "catalog"), []string{"applyCatalog", "fieldOf", "eventLabel", "loopEvent"},
		`applyCatalog({ events: [{ provider: "github", kind: "round_blocked", label: "round blocked", loop: true }] });
process.stdout.write(JSON.stringify({ label: eventLabel("round_blocked"), loop: loopEvent("round_blocked") }));`, nil, &got)
	if got.Label != "round blocked" || !got.Loop {
		t.Fatalf("%+v", got)
	}
	if strings.Contains(appJS(t), "round_blocked") {
		t.Error("app.js names a GitHub event kind")
	}
}

func TestCockpitListsTheFailingChecks(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler("").ServeHTTP(rec, httptest.NewRequest("GET", "/static/app.js", nil))
	for _, want := range []string{"function checksBlock", `unitPath(keyUnit(pr), "checks")`, "c.steps", `if (d.row.kind === "checks") loadChecks(pr);`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("app.js lacks %s", want)
		}
	}
}

func TestCockpitNeverShowsTheCardCommentAsAQuestion(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler("").ServeHTTP(rec, httptest.NewRequest("GET", "/static/app.js", nil))
	for _, gone := range []string{"function asksBlock", "workspace.comment"} {
		if strings.Contains(rec.Body.String(), gone) {
			t.Errorf("app.js still has %s", gone)
		}
	}
}

func TestCockpitShowsTheTurn(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler("").ServeHTTP(rec, httptest.NewRequest("GET", "/static/app.js", nil))
	body := rec.Body.String()
	for _, want := range []string{"function questionBlock", "function artifactBlock", "function optionButton", "questionBlock(d)",
		`"continue", "Continue"`, `"dismiss", "Dismiss"`, `source: "cockpit"`, "body.option_id",
		`turn_sent: "turn sent"`, `turn_started: "turn started"`, `turn_ended: "turn ended"`, `turn_resumed: "turn resumed"`, "timelineEvents(d)"} {
		if !strings.Contains(body, want) {
			t.Errorf("app.js lacks %s", want)
		}
	}
	rec = httptest.NewRecorder()
	Handler("").ServeHTTP(rec, httptest.NewRequest("GET", "/static/app.css", nil))
	for _, want := range []string{".d-question", ".d-artifact", ".act-note"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("app.css lacks %s", want)
		}
	}
}

func TestCockpitShowsTheBrief(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler("").ServeHTTP(rec, httptest.NewRequest("GET", "/static/app.js", nil))
	body := rec.Body.String()
	for _, want := range []string{"function briefBlock", "briefBlock(d.brief, r, readLens())", "open_questions", "risk_why", `"Brief pending"`, "stale", `["low", "medium", "high"]`} {
		if !strings.Contains(body, want) {
			t.Errorf("app.js lacks %s", want)
		}
	}
	for _, raw := range []string{"innerHTML", "outerHTML", "insertAdjacentHTML"} {
		if strings.Contains(body, raw) {
			t.Errorf("app.js writes HTML through %s", raw)
		}
	}
	rec = httptest.NewRecorder()
	Handler("").ServeHTTP(rec, httptest.NewRequest("GET", "/static/app.css", nil))
	css := rec.Body.String()
	_, rule, _ := strings.Cut(css, ".d-brief {")
	rule, _, _ = strings.Cut(rule, "}")
	if !strings.Contains(rule, "overflow-wrap: anywhere") || !strings.Contains(css, ".risk-high {") || !strings.Contains(css, ".risk-medium {") {
		t.Errorf("app.css: .d-brief {%s}", rule)
	}
}

func TestCockpitHasTheStalledBoard(t *testing.T) {
	h := Handler("")
	for path, wants := range map[string][]string{
		"/static/app.js":  {"function renderBoard", `layout: p.get("layout") === "board"`, `f.id === "lane"`, `"aria-pressed"`},
		"/":               {`id="board"`},
		"/static/app.css": {".board", ".card", ".lane-head"},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		for _, want := range wants {
			if !strings.Contains(rec.Body.String(), want) {
				t.Errorf("%s lacks %s", path, want)
			}
		}
	}
}

func TestCockpitHasTheFilter(t *testing.T) {
	h := Handler("")
	for path, wants := range map[string][]string{
		"/static/app.js": {"function fold", "function terms", "function matches", "function marked", "function rowByURL",
			"function matchesActivity", `p.get("q")`, "history.replaceState", "function typing", "function emptyNote"},
		"/static/keys.js": {`filter: () => {`, `getElementById("search")`},
		"/":               {`id="search"`, `type="search"`, `aria-keyshortcuts="/"`},
		"/static/app.css": {".search", "mark {", "--mark"},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		for _, want := range wants {
			if !strings.Contains(rec.Body.String(), want) {
				t.Errorf("%s lacks %s", path, want)
			}
		}
	}
}

func TestCockpitHasExplore(t *testing.T) {
	h := Handler("")
	for path, wants := range map[string][]string{
		"/static/app.js": {"function renderExplore", "function explore(", `viewOfExplore("explore")`, `viewOfExplore("explore.recent")`, "function actOnCandidate", `actionButtons(c, "explore"`, `"/api/actions"`, "function loadSpecs",
			"function candidateItem", `p.get("x")`, "r.read", "r.empty", "exploreSearch().sorts", "candidateWait(c, row,", "stalled_review_hours"},
		"/":               {`id="explore"`, `id="explore-form"`, `id="explore-q"`, `id="explore-guidance"`, `id="explore-results"`},
		"/static/app.css": {".cand", ".chip", ".explore-act", ".cand .wait.late"},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		for _, want := range wants {
			if !strings.Contains(rec.Body.String(), want) {
				t.Errorf("%s lacks %s", path, want)
			}
		}
	}
}

func TestHiddenBeatsEveryDisplayRule(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler("").ServeHTTP(rec, httptest.NewRequest("GET", "/static/app.css", nil))
	if !strings.Contains(rec.Body.String(), "[hidden] { display: none !important; }") {
		t.Fatal("app.css lets a display rule show an element marked hidden")
	}
}

func TestGuidanceSitsAboveTheActions(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler("").ServeHTTP(rec, httptest.NewRequest("GET", "/static/app.js", nil))
	if !strings.Contains(rec.Body.String(), "box.replaceChildren(...[top, guidance, acts, log].filter(Boolean))") {
		t.Fatal("the detail panel does not put guidance between the facts and the actions")
	}
}

func TestRefusalDetailIsNamedAfterTheWorkspaceManager(t *testing.T) {
	src := appJS(t)
	if want := `t.source ? ((state.health && state.health.workspace_manager) || t.source) + "'s message" : "Details"`; !strings.Contains(src, want) {
		t.Fatalf("app.js lacks %s", want)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on PATH")
	}
	script := `const label = (state, t) => t.source ? ((state.health && state.health.workspace_manager) || t.source) + "'s message" : "Details";
process.stdout.write(JSON.stringify([label({ health: { workspace_manager: "Orca" } }, { source: "orca" }), label({}, { source: "orca" }), label({ health: {} }, {})]));`
	out, err := exec.Command(node, "-e", script).Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	if got := string(out); got != `["Orca's message","orca's message","Details"]` {
		t.Fatalf("labels %s", got)
	}
}

func TestTheScriptsParse(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on PATH")
	}
	for _, name := range []string{"app.js", "dash.js", "keys.js", "palette.js", "routes.js", "views.js", "task.js", "review.js", "detail.js"} {
		if out, err := exec.Command(node, "--check", "static/"+name).CombinedOutput(); err != nil {
			t.Errorf("%s: %v: %s", name, err, out)
		}
	}
}

// detailWidthCSS stands in for a stylesheet whose --detail-width is width.
func detailWidthCSS(width string) string {
	return `globalThis.document = { documentElement: {} };
globalThis.getComputedStyle = () => ({ getPropertyValue: (name) => name === "--detail-width" ? " ` + width + `" : "" });
`
}

func TestDetailWidthClamp(t *testing.T) {
	type in struct {
		Px       float64 `json:"px"`
		Viewport float64 `json:"viewport"`
	}
	cases := []in{{380, 1600}, {100, 1600}, {5000, 1600}, {960, 1600}, {961, 1600}, {500, 400}, {299.6, 1600}}
	want := []float64{380, 300, 960, 960, 960, 300, 300}
	src := appJS(t)
	prelude := detailWidthCSS("380px") + jsConst(t, src, "DETAIL_WIDTH_MIN")
	var got []float64
	nodeRun(t, prelude, []string{"defaultDetailWidth", "clampDetailWidth"},
		`process.stdout.write(JSON.stringify(input.map((c) => clampDetailWidth(c.px, c.viewport))));`, cases, &got)
	for i := range cases {
		if got[i] != want[i] {
			t.Errorf("%+v: got %v, want %v", cases[i], got[i], want[i])
		}
	}
}

func TestDetailWidthRestore(t *testing.T) {
	type in struct {
		Stored string `json:"stored"`
		Throws bool   `json:"throws"`
	}
	cases := []in{{Stored: ""}, {Stored: "520"}, {Stored: "50"}, {Stored: "99999"}, {Stored: "junk"}, {Stored: "520", Throws: true}}
	want := []float64{412, 520, 300, 960, 412, 412}
	src := appJS(t)
	prelude := jsConst(t, src, "DETAIL_WIDTH_KEY") + detailWidthCSS("412px") + jsConst(t, src, "DETAIL_WIDTH_MIN")
	var got []float64
	nodeRun(t, prelude, []string{"defaultDetailWidth", "clampDetailWidth", "storedDetailWidth", "saveDetailWidth"}, `
process.stdout.write(JSON.stringify(input.map((c) => {
  globalThis.localStorage = {
    getItem: () => { if (c.throws) throw new Error("denied"); return c.stored === "" ? null : c.stored; },
    setItem: () => { throw new Error("denied"); },
    removeItem: () => { throw new Error("denied"); },
  };
  saveDetailWidth(400);
  saveDetailWidth(null);
  return storedDetailWidth(1600);
})));`, cases, &got)
	for i := range cases {
		if got[i] != want[i] {
			t.Errorf("%+v: got %v, want %v", cases[i], got[i], want[i])
		}
	}
}

func TestDetailGridUsesWidthVariable(t *testing.T) {
	raw, err := os.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(raw)
	if !strings.Contains(css, "body.has-detail #app { grid-template-columns: var(--rail-width) minmax(0, 1fr) var(--detail-width); }") {
		t.Error("the detail grid column does not come from --detail-width")
	}
	if !strings.Contains(css, "#detail-resize, body.has-detail #detail-resize { display: none; }") {
		t.Error("the handle is not hidden in the stacked layout")
	}
}

func TestDetailWidthSurvivesShrinkThenGrow(t *testing.T) {
	src := appJS(t)
	prelude := jsConst(t, src, "DETAIL_WIDTH_KEY") + detailWidthCSS("380px") +
		jsConst(t, src, "DETAIL_WIDTH_MIN") + jsConst(t, src, "DETAIL_WIDTH_STEP") + `
const listeners = {};
const attrs = {};
const style = {};
const handle = {
  addEventListener: (n, f) => { listeners[n] = f; },
  setAttribute: (k, v) => { attrs[k] = v; },
};
globalThis.document = { getElementById: () => handle, documentElement: { style: { setProperty: (k, v) => { style[k] = v; } } } };
const winListeners = {};
globalThis.window = { innerWidth: 1600, addEventListener: (n, f) => { winListeners[n] = f; } };
globalThis.localStorage = { getItem: () => "900", setItem: () => {}, removeItem: () => {} };
`
	var got []string
	nodeRun(t, prelude, []string{"defaultDetailWidth", "clampDetailWidth", "storedDetailWidth", "saveDetailWidth", "setupDetailResize"}, `
setupDetailResize();
const seen = [style["--detail-width"]];
window.innerWidth = 700; winListeners.resize(); seen.push(style["--detail-width"]);
window.innerWidth = 1600; winListeners.resize(); seen.push(style["--detail-width"]);
process.stdout.write(JSON.stringify(seen));`, struct{}{}, &got)
	want := []string{"900px", "420px", "900px"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestDetailWidthWithoutTheStylesheetValueIsTheMinimum(t *testing.T) {
	src := appJS(t)
	prelude := detailWidthCSS("") + jsConst(t, src, "DETAIL_WIDTH_MIN")
	var got []float64
	nodeRun(t, prelude, []string{"defaultDetailWidth", "clampDetailWidth"},
		`process.stdout.write(JSON.stringify([defaultDetailWidth(), clampDetailWidth(NaN, 1600)]));`, struct{}{}, &got)
	if got[0] != 300 || got[1] != 300 {
		t.Errorf("got %v, want the 300 minimum", got)
	}
}

func TestDetailWidthDoubleClickRestoresTheStylesheetDefault(t *testing.T) {
	src := appJS(t)
	prelude := jsConst(t, src, "DETAIL_WIDTH_KEY") + jsConst(t, src, "DETAIL_WIDTH_MIN") + jsConst(t, src, "DETAIL_WIDTH_STEP") + `
const listeners = {};
const style = {};
const handle = {
  addEventListener: (n, f) => { listeners[n] = f; },
  setAttribute: () => {},
};
globalThis.document = { getElementById: () => handle, documentElement: { style: {
  setProperty: (k, v) => { style[k] = v; },
  removeProperty: (k) => { delete style[k]; },
} } };
globalThis.getComputedStyle = () => ({ getPropertyValue: (name) => name === "--detail-width" ? (style[name] ?? "380px") : "" });
globalThis.window = { innerWidth: 1600, addEventListener: () => {} };
let stored = "900";
globalThis.localStorage = { getItem: () => stored, setItem: (k, v) => { stored = v; }, removeItem: () => { stored = null; } };
`
	var got []any
	nodeRun(t, prelude, []string{"defaultDetailWidth", "clampDetailWidth", "storedDetailWidth", "saveDetailWidth", "setupDetailResize"}, `
setupDetailResize();
listeners.dblclick();
process.stdout.write(JSON.stringify([style["--detail-width"], stored]));`, struct{}{}, &got)
	if got[0] != "380px" || got[1] != nil {
		t.Errorf("after a double click the width is %v and the stored one %v, want 380px and none", got[0], got[1])
	}
}
