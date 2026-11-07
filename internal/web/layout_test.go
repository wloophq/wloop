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
	chrome := headlessChrome()
	if chrome == "" {
		t.Skip("no Chrome to lay the page out")
	}
	css, err := filepath.Abs("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	page := `<!doctype html><html><head><meta charset="utf-8"><link rel="stylesheet" href="file://` + css + `"><style>body { width: ` + itoa(width) + `px; margin: 0; }</style></head><body>` +
		`<section id="list"><details id="list-box" class="list-box" open><summary id="list-sum" class="list-sum"></summary><div id="table"><div class="rows">` + rows + `</div></div></details></section>` +
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
