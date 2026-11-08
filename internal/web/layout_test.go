package web

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// headlessChrome is the path of a Chrome the layout tests drive, "" when none is installed.
func headlessChrome() string {
	for _, p := range []string{"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "google-chrome", "chromium"} {
		if path, err := exec.LookPath(p); err == nil {
			return path
		}
	}
	return ""
}

// layoutOf renders rows, the HTML of list rows, in a list width px wide in a window viewport px wide, and
// returns what probe, a script over the page, writes.
func layoutOf(t *testing.T, rows string, viewport, width int, probe string) string {
	t.Helper()
	return pageProbe(t, `<body><style>body { width: `+itoa(width)+`px; margin: 0; }</style>`+
		`<section id="list"><details id="list-box" class="list-box" open><summary id="list-sum" class="list-sum"></summary><div id="table"><div class="rows">`+rows+`</div></div></details></section>`, viewport, probe)
}

// pageProbe lays body, a page's body opening tag and content, out with the cockpit's stylesheet in a window
// viewport px wide, and returns what probe, a script over the page, writes.
func pageProbe(t *testing.T, body string, viewport int, probe string) string {
	t.Helper()
	chrome := headlessChrome()
	if chrome == "" {
		t.Skip("no Chrome to lay the page out")
	}
	css, err := filepath.Abs("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	page := `<!doctype html><html><head><meta charset="utf-8"><link rel="stylesheet" href="file://` + css + `"></head>` + body +
		`<pre id="probe"></pre><script>document.getElementById("probe").textContent = (function () {` + probe + `})();</script></body></html>`
	file := filepath.Join(dir, "page.html")
	if err := os.WriteFile(file, []byte(page), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, chrome, "--headless=new", "--disable-gpu", "--allow-file-access-from-files", "--user-data-dir="+filepath.Join(dir, "profile"),
		"--window-size="+itoa(viewport)+",800",
		"--dump-dom", "file://"+file).Output()
	if err != nil {
		t.Fatalf("chrome: %v", err)
	}
	m := regexp.MustCompile(`(?s)<pre id="probe">(.*?)</pre>`).FindSubmatch(out)
	if m == nil {
		t.Fatalf("no probe in %s", out)
	}
	return string(m[1])
}

// TestTheDetailOfANarrowWindowLeavesItsTitleBarClear lays a native window's page out at widths where the
// detail covers the window: the detail starts below the title bar, whose controls the window draws over the
// page, and fills the rest; a browser's detail still covers the whole page.
func TestTheDetailOfANarrowWindowLeavesItsTitleBarClear(t *testing.T) {
	const page = `<header id="top"><span class="tb-title">A task</span></header><main id="app"><section id="detail"><div class="d-head"><h1>A task</h1></div></section></main>`
	const probe = `const r = (el) => el.getBoundingClientRect();
return JSON.stringify({ Top: r(document.getElementById("top")).bottom, Detail: r(document.getElementById("detail")).top, Height: r(document.getElementById("detail")).bottom });`
	for _, c := range []struct {
		body     string
		viewport int
		clear    bool
	}{{`<body class="titlebar has-detail">`, 600, true}, {`<body class="titlebar has-detail layout-detail">`, 480, true}, {`<body class="has-detail">`, 600, false}} {
		var got struct{ Top, Detail, Height float64 }
		if err := json.Unmarshal([]byte(pageProbe(t, c.body+page, c.viewport, probe)), &got); err != nil {
			t.Fatal(err)
		}
		if c.clear && (got.Top <= 0 || got.Detail < got.Top) {
			t.Errorf("%s at %dpx: the detail starts at %v, over the title bar that ends at %v", c.body, c.viewport, got.Detail, got.Top)
		}
		if !c.clear && got.Detail != 0 {
			t.Errorf("%s at %dpx: the detail starts at %v, want the whole page", c.body, c.viewport, got.Detail)
		}
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// shotOf saves a screenshot of rows laid out as layoutOf lays them to path, when the run asks for shots.
func shotOf(t *testing.T, rows string, viewport, width int, path string) {
	t.Helper()
	dir := os.Getenv("LAYOUT_SHOTS")
	if dir == "" {
		return
	}
	css, err := filepath.Abs("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	page := `<!doctype html><html><head><meta charset="utf-8"><link rel="stylesheet" href="file://` + css + `"><style>body { width: ` + itoa(width) + `px; margin: 0; }</style></head><body>` +
		`<section id="list"><details id="list-box" class="list-box" open><summary id="list-sum" class="list-sum"></summary><div id="table"><div class="rows">` + rows + `</div></div></details></section></body></html>`
	file := filepath.Join(tmp, "page.html")
	if err := os.WriteFile(file, []byte(page), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if out, err := exec.CommandContext(ctx, headlessChrome(), "--headless=new", "--disable-gpu", "--allow-file-access-from-files", "--hide-scrollbars",
		"--user-data-dir="+filepath.Join(tmp, "profile"), "--window-size="+itoa(viewport)+",140", "--screenshot="+filepath.Join(dir, path), "file://"+file).CombinedOutput(); err != nil {
		t.Fatalf("chrome: %v: %s", err, out)
	}
}

// rowHTML renders pr, a wire row, with the served catalog under change and the action specs given, as the
// cockpit draws it in view.
func rowHTML(t *testing.T, pr map[string]any, specs map[string]any, view string, change func(cat map[string]any)) string {
	t.Helper()
	var cat map[string]any
	if err := json.Unmarshal([]byte(servedCatalog(t)), &cat); err != nil {
		t.Fatal(err)
	}
	change(cat)
	raw, err := json.Marshal(cat)
	if err != nil {
		t.Fatal(err)
	}
	var got struct{ HTML string }
	rowViewRun(t, raw, `state.rows = [input.pr];
state.specs = { version: "", byId: input.specs };
const lens = lensOf({ ...readHash(), view: input.view });
const esc = (s) => String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/"/g, "&quot;");
const ser = (n) => n instanceof Text ? esc(n.data) : "<" + n.tag + Object.entries(n.attrs).map(([k, v]) => " " + k + "=\"" + esc(v) + "\"").join("") + ">" + n.children.map(ser).join("") + "</" + n.tag + ">";
process.stdout.write(JSON.stringify({ HTML: ser(rowView(rowModel(input.pr, "", lens, ""), lens)) }));`,
		map[string]any{"pr": pr, "specs": specs, "view": view, "now": pr["now"]}, &got)
	return got.HTML
}

// ptBR words the served catalog's situation of no reviewer as the user's locale does.
func ptBR(cat map[string]any) {
	for _, s := range cat["situations"].([]any) {
		if sit := s.(map[string]any); sit["kind"] == "no_reviewer" {
			sit["label"] = "sem revisor"
		}
	}
	for _, f := range cat["facets"].([]any) {
		if spec := f.(map[string]any); spec["id"] == "kind" {
			labels, _ := spec["labels"].(map[string]any)
			if labels == nil {
				labels = map[string]any{}
				spec["labels"] = labels
			}
			labels["no_reviewer"] = "sem revisor"
		}
	}
}

// usersRow is the row a user saw broken in My PRs: a pull request of theirs, stalled, waiting on them, with no
// reviewer, a long title, its brief's type, risk and size, a dozen tags and eight actions, in pt-BR words.
func usersRow(t *testing.T) string {
	t.Helper()
	pr := lensRows()[1]
	pr["kind"], pr["waiting_on"], pr["stalled"] = "no_reviewer", "me", true
	pr["title"] = "fix(parts): resolve messages delivered out of order by the unary ack consumer when the broker redelivers"
	pr["brief"] = map[string]any{"risk": "medium", "size": "m", "kind": "fix", "open_questions": 2, "tags": []string{"area:messaging", "audience:developers",
		"audience:operators", "change:fix", "concern:concurrency", "concern:reliability", "domain:parts-matching", "lang:python", "tool:gcp-pubsub",
		"infra:queue", "infra:db", "prio:high", "customer:acme"}}
	pr["views"] = []string{"mine", "stalled"}
	actions := []map[string]any{}
	specs := map[string]any{}
	for _, a := range [][2]string{{"review", "Review"}, {"address", "Address"}, {"act", "Agir"}, {"nudge", "Cutucar"}, {"pause", "Pausar"}, {"mute", "Silenciar"},
		{"open", "Abrir"}, {"remove", "Remover"}} {
		actions = append(actions, map[string]any{"id": a[0], "enabled": true})
		specs[a[0]] = map[string]any{"id": a[0], "label": a[1], "group": "control", "surfaces": []string{"row"}}
	}
	pr["actions"] = actions
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	row := wireRow("me", now, pr)
	row["now"] = now.UnixMilli()
	html := rowHTML(t, row, specs, "mine", ptBR)
	for _, want := range []string{`class="row w-me mine late"`, ">sem revisor<", ">fix<", "risk medium", ">M<", ">Remover<"} {
		if !strings.Contains(html, want) {
			t.Fatalf("the user's row lacks %s: %s", want, html)
		}
	}
	return html
}

// rowBoxes is a probe of the first row: its title's share of the row, where its first line, situation and
// meta are, how many of its actions show whole, the pieces of its meta cut mid-word, and its +n chip.
const rowBoxes = `
const r = (el) => el.getBoundingClientRect();
const row = document.querySelector(".row");
const metaEl = row.querySelector(":scope > .meta");
const meta = r(metaEl);
const inside = (b, c) => b.width > 0 && b.left >= c.left - 0.5 && b.right <= c.right + 0.5 && b.top >= c.top - 0.5 && b.bottom <= c.bottom + 0.5;
const clipOf = (el) => { for (let p = el.parentElement; p && p !== row; p = p.parentElement) if (getComputedStyle(p).overflow !== "visible") return r(p); return r(row); };
const seenIn = (b, c) => b.width > 0 && b.right > c.left && b.left < c.right && b.bottom > c.top && b.top < c.bottom;
const acts = [...metaEl.querySelectorAll(".act")].filter((a) => getComputedStyle(a).display !== "none" && seenIn(r(a), clipOf(a)));
const whole = acts.filter((a) => inside(r(a), clipOf(a)) && inside(r(a), r(row))).length;
const pieces = [...metaEl.querySelectorAll(".pill, .flag")].filter((p) => getComputedStyle(p).display !== "none");
const cut = pieces.filter((p) => {
  const b = r(p), c = clipOf(p);
  return seenIn(b, c) && (!inside(b, c) || p.scrollWidth > p.clientWidth + 1);
}).map((p) => p.textContent);
const more = [...metaEl.querySelectorAll("[data-more]")].map((m) => m.textContent + "|" + (m.getAttribute("title") || ""));
return JSON.stringify({ Title: r(row.querySelector(".t")).width, Row: r(row).width, L1Right: r(row.querySelector(".l1")).right, MetaLeft: meta.left,
  VerbRight: r(row.querySelector(".l1 .verb")).right, Acts: acts.length, Whole: whole, Cut: cut, More: more, TitleWhole: row.querySelector(".t").scrollWidth <= row.querySelector(".t").clientWidth + 1 });`

type rowBox struct {
	Title, Row, L1Right, MetaLeft, VerbRight float64
	Acts, Whole                              int
	Cut, More                                []string
	TitleWhole                               bool
}

// TestTheUsersRowReadsWholeAtEveryWidth lays out the user's row at the widths it is seen at: its title keeps a
// fair share of the first line, nothing of the first line paints under the meta, no piece of the meta is cut
// mid-word, the tags past the first two collapse into a +n chip that names them, and every action it shows
// shows whole.
func TestTheUsersRowReadsWholeAtEveryWidth(t *testing.T) {
	html := usersRow(t)
	for _, c := range []struct{ viewport, width int }{{1906, 1906}, {1280, 1280}, {1280, 760}, {480, 480}} {
		shotOf(t, html, c.viewport, c.width, "row-"+itoa(c.width)+".png")
		var box rowBox
		raw := layoutOf(t, html, c.viewport, c.width, rowBoxes)
		if err := json.Unmarshal([]byte(raw), &box); err != nil {
			t.Fatalf("%v: %s", err, raw)
		}
		share := 0.3
		if c.width < 900 {
			share = 0.25
		}
		if c.width >= 1900 && !box.TitleWhole {
			t.Errorf("%dpx: the title is cut although the row has room for it: %+v", c.width, box)
		}
		if box.Title < share*box.Row || box.L1Right > box.MetaLeft || box.VerbRight > box.MetaLeft || box.Whole != box.Acts || len(box.Cut) > 0 {
			t.Errorf("%dpx list in a %dpx window: %+v", c.width, c.viewport, box)
		}
		if c.viewport > 900 && box.Acts == 0 {
			t.Errorf("%dpx window: the row shows no action: %+v", c.viewport, box)
		}
		if c.width > 600 && (len(box.More) != 1 || !strings.HasPrefix(box.More[0], "+11|") || !strings.Contains(box.More[0], "customer:acme")) {
			t.Errorf("%dpx: the tags past two collapse into one +n chip that names them: %v", c.width, box.More)
		}
	}
}

// TestAHiddenPieceOrActionLeavesTheFocusOrder lays the user's row out where some of its pieces and actions
// wrap out of sight: those are inert, so neither Tab nor the row's arrows reach them, and every one shown is not.
func TestAHiddenPieceOrActionLeavesTheFocusOrder(t *testing.T) {
	html := usersRow(t)
	probe := jsFunction(t, "\n"+staticJS(t, "row.js"), "markWrapped") + `
const row = document.querySelector(".row");
row.querySelectorAll(".pieces, .acts").forEach(markWrapped);
const r = (el) => el.getBoundingClientRect();
const out = { Hidden: 0, Shown: 0, Wrong: [] };
for (const box of row.querySelectorAll(".pieces, .acts")) {
  const c = r(box);
  for (const k of box.children) {
    const b = r(k);
    const seen = b.bottom <= c.bottom + 0.5 && b.top >= c.top - 0.5;
    if (seen) out.Shown++; else out.Hidden++;
    if (k.inert === seen) out.Wrong.push(k.textContent + (k.inert ? " inert" : " live"));
  }
}
return JSON.stringify(out);`
	var got struct {
		Hidden, Shown int
		Wrong         []string
	}
	if err := json.Unmarshal([]byte(layoutOf(t, html, 1280, 760, probe)), &got); err != nil {
		t.Fatal(err)
	}
	if got.Hidden == 0 || got.Shown == 0 || len(got.Wrong) > 0 {
		t.Fatalf("%+v: what wraps out of sight is inert, what shows is not", got)
	}
}

// panelPage is the floating panel's page as the omnibox draws it: the box alone, without the overlay's styles.
const panelPage = `<body class="panel-omnibox"><div class="pal omni omni-panel"><input class="omni-input"><div class="omni-results"><div role="option">A task</div></div><p class="omni-keys">Esc</p></div>`

// TestTheFloatingPanelDragsFromItsEdges lays the floating panel out: its frame and its keys hint move the
// window, while its field and its results take the mouse as they would anywhere.
func TestTheFloatingPanelDragsFromItsEdges(t *testing.T) {
	const probe = `const drag = (s) => getComputedStyle(document.querySelector(s)).getPropertyValue("--wails-draggable").trim();
return JSON.stringify({ Box: drag(".omni-panel"), Keys: drag(".omni-keys"), Input: drag(".omni-input"), Option: drag("[role=option]") });`
	var got struct{ Box, Keys, Input, Option string }
	if err := json.Unmarshal([]byte(pageProbe(t, panelPage, 680, probe)), &got); err != nil {
		t.Fatal(err)
	}
	if got.Box != "drag" || got.Keys != "drag" || got.Input != "no-drag" || got.Option != "no-drag" {
		t.Fatalf("%+v: the frame and the hint drag the panel, the field and the results do not", got)
	}
}

// TestTheFloatingPanelsFieldSpansIt lays the floating panel out: its field spans the panel in a large type,
// and its keys hint sits at the bottom whatever the results hold.
func TestTheFloatingPanelsFieldSpansIt(t *testing.T) {
	const probe = `const r = (s) => document.querySelector(s).getBoundingClientRect();
return JSON.stringify({ Panel: r(".omni-panel").width, Field: r(".omni-input").width, Font: parseFloat(getComputedStyle(document.querySelector(".omni-input")).fontSize),
  Height: r(".omni-input").height, Keys: r(".omni-keys").bottom, Bottom: r(".omni-panel").bottom });`
	var got struct{ Panel, Field, Font, Height, Keys, Bottom float64 }
	if err := json.Unmarshal([]byte(pageProbe(t, panelPage, 680, probe)), &got); err != nil {
		t.Fatal(err)
	}
	if got.Field < 0.9*got.Panel || got.Font < 18 || got.Height < 40 || got.Keys < got.Bottom-1 {
		t.Fatalf("%+v: the field spans the panel in a large type and the keys hint sits at its bottom", got)
	}
}

// TestAPlanGateCardReadsWithinTheQueue lays a plan gate's card out at the queue's width and at a detail's: its
// summary reads as body text, its target and why as meta lines, the why folded behind a styled toggle, and a long
// fact value cut with an ellipsis rather than widening the card. On the plan page a workflow's summary is not bold.
func TestAPlanGateCardReadsWithinTheQueue(t *testing.T) {
	long := strings.Repeat("acme/api#7-retry-uploads-", 20)
	card := `<article class="ask d-gate" data-kind="plan"><header class="hd"><i class="dot"></i><h3 class="d-sub">Run this?</h3></header><div class="bd">` +
		`<div class="gate-plan"><p class="plan-summary">Close the task once its asker checked the answer; posts nothing</p>` +
		`<p class="meta plan-on">Acts on <a href="https://chat.example/c1">Retry flaky uploads</a></p>` +
		`<details class="plan-why" open><summary>Why it fired</summary><ul><li>task.link_signal = ` + long + `</li><li>Category: Active</li></ul></details></div>` +
		`</div></article>`
	plan := `<ol class="plan-items"><li class="plan-item pending"><div class="plan-line"><span class="plan-when">waits</span><span class="plan-wf">Close the task once its asker checked the answer; posts nothing</span></div></li></ol>`
	const probe = `const q = (s) => document.querySelector(s);
const cs = (s) => getComputedStyle(q(s));
const li = q(".plan-why li");
return JSON.stringify({ Card: q(".d-gate").getBoundingClientRect().width, Scroll: q(".d-gate").scrollWidth, Client: q(".d-gate").clientWidth,
  Li: li.getBoundingClientRect().width, Ellipsis: cs(".plan-why li").textOverflow, Wrap: cs(".plan-why li").whiteSpace,
  Toggle: cs(".plan-why summary").cursor, ToggleSize: parseFloat(cs(".plan-why summary").fontSize), OnSize: parseFloat(cs(".plan-on").fontSize),
  Body: parseFloat(cs(".plan-summary").fontSize), Margin: parseFloat(cs(".plan-summary").marginTop), WF: cs(".plan-wf").fontWeight });`
	for _, width := range []int{768, 360} {
		page := `<body style="margin: 0"><div class="queue-screen" style="width: ` + itoa(width) + `px; box-sizing: border-box"><div class="queue">` + card + `</div>` + plan + `</div>`
		var got struct {
			Card, Scroll, Client, Li, ToggleSize, OnSize, Body, Margin float64
			Ellipsis, Wrap, Toggle, WF                                 string
		}
		if err := json.Unmarshal([]byte(pageProbe(t, page, width+40, probe)), &got); err != nil {
			t.Fatal(err)
		}
		switch {
		case got.Scroll > got.Client || got.Li > got.Card:
			t.Errorf("%dpx: %+v, want the card no wider than the queue and the fact cut", width, got)
		case got.Ellipsis != "ellipsis" || got.Wrap != "nowrap":
			t.Errorf("%dpx: a fact value overflows as %q/%q, want one line cut with an ellipsis", width, got.Ellipsis, got.Wrap)
		case got.Toggle != "pointer" || got.ToggleSize >= got.Body || got.OnSize >= got.Body:
			t.Errorf("%dpx: %+v, want the why's toggle and the target as meta under the body-sized summary", width, got)
		case got.Margin > 8:
			t.Errorf("%dpx: the summary's margin %v, want the card's own rhythm", width, got.Margin)
		case got.WF != "400":
			t.Errorf("%dpx: the plan page's workflow summary weighs %s, want regular", width, got.WF)
		}
	}
}

// TestTheHomeSpansTheList lays the home out in a wide list: it takes the list's whole width, which only the
// detail opening beside it narrows.
func TestTheHomeSpansTheList(t *testing.T) {
	const page = `<body style="margin: 0"><section id="list" style="width: 1600px"><section id="home" class="home-screen"><div class="home"><p class="briefing">The agent has 3 questions for you.</p></div></section></section>`
	const probe = `const r = (s) => document.querySelector(s).getBoundingClientRect();
return JSON.stringify({ Screen: r(".home-screen").width, Home: r(".home").width });`
	var got struct{ Screen, Home float64 }
	if err := json.Unmarshal([]byte(pageProbe(t, page, 1700, probe)), &got); err != nil {
		t.Fatal(err)
	}
	if got.Home < got.Screen-2*16-1 {
		t.Fatalf("%+v: the home spans its screen", got)
	}
}
