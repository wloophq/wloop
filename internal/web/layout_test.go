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
		`<section id="list"><details id="list-box" class="list-box" open><div id="table"><div class="rows">` + rows + `</div></div></details></section>` +
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

// usersRow is the row a user saw lose its first line: a pull request of theirs, stalled, waiting on them,
// with no reviewer, a long title, its brief's type, risk and size, a dozen tags and its actions, in pt-BR words.
func usersRow(t *testing.T) string {
	t.Helper()
	pr := lensRows()[1]
	pr["kind"], pr["waiting_on"], pr["stalled"] = "no_reviewer", "me", true
	pr["title"] = "Adicionar novas tentativas ao envio para que redes instáveis parem de derrubar o lote inteiro de exportação"
	tags := []string{}
	for _, tag := range []string{"lang:go", "lang:sql", "change:fix", "domain:billing", "area:uploads", "team:platform", "infra:queue", "infra:db", "kind:bug",
		"prio:high", "customer:acme", "area:export", "risk:data"} {
		tags = append(tags, tag)
	}
	pr["brief"] = map[string]any{"risk": "medium", "size": "m", "kind": "fix", "tags": tags}
	pr["views"] = []string{"mine", "stalled"}
	actions := []map[string]any{}
	specs := map[string]any{}
	for id, label := range map[string]string{"request-review": "Pedir revisão", "pause": "Pausar", "dismiss": "Dispensar", "open": "Abrir"} {
		actions = append(actions, map[string]any{"id": id, "enabled": true})
		specs[id] = map[string]any{"id": id, "label": label, "group": "control", "surfaces": []string{"row"}}
	}
	pr["actions"] = actions
	var cat map[string]any
	if err := json.Unmarshal([]byte(servedCatalog(t)), &cat); err != nil {
		t.Fatal(err)
	}
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
	raw, err := json.Marshal(cat)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	var got struct{ HTML string }
	rowViewRun(t, raw, `state.rows = [input.pr];
state.specs = { version: "", byId: input.specs };
const lens = lensOf({ ...readHash(), view: "mine" });
const esc = (s) => String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/"/g, "&quot;");
const ser = (n) => n instanceof Text ? esc(n.data) : "<" + n.tag + Object.entries(n.attrs).map(([k, v]) => " " + k + "=\"" + esc(v) + "\"").join("") + ">" + n.children.map(ser).join("") + "</" + n.tag + ">";
process.stdout.write(JSON.stringify({ HTML: ser(rowView(rowModel(input.pr, "", lens, ""), lens)) }));`,
		map[string]any{"pr": wireRow("me", now, pr), "specs": specs, "now": now.UnixMilli()}, &got)
	for _, want := range []string{`class="row w-me mine late"`, ">sem revisor<", ">fix<", "risk medium", ">M<", "Pedir revisão"} {
		if !strings.Contains(got.HTML, want) {
			t.Fatalf("the user's row lacks %s: %s", want, got.HTML)
		}
	}
	return got.HTML
}

// rowBoxes is a probe of the first row: its title's width, where its first line, situation and meta are, and
// how many of its actions show whole inside the row.
const rowBoxes = `
const r = (el) => el.getBoundingClientRect();
const row = document.querySelector(".row");
const meta = r(row.querySelector(":scope > .meta"));
const acts = [...row.querySelectorAll(":scope > .meta .act")];
const shown = acts.filter((a) => getComputedStyle(a).display !== "none");
const whole = shown.filter((a) => r(a).width > 0 && r(a).left >= meta.left - 0.5 && r(a).right <= Math.min(meta.right, r(row).right) + 0.5).length;
return JSON.stringify({ Title: r(row.querySelector(".t")).width, L1Right: r(row.querySelector(".l1")).right, MetaLeft: meta.left,
  VerbRight: r(row.querySelector(".l1 .verb")).right, Acts: shown.length, Whole: whole });`

// TestTheUsersRowKeepsItsFirstLineAndItsActions lays out the user's row in a narrow window, where the row
// hides its actions, and in a wide window with a narrow list, where it shows them: either way its first line
// keeps room for its situation and title, nothing of it paints under the meta, and every action it shows
// shows whole.
func TestTheUsersRowKeepsItsFirstLineAndItsActions(t *testing.T) {
	html := usersRow(t)
	for _, c := range []struct{ viewport, width int }{{480, 480}, {1280, 760}} {
		var box struct {
			Title, L1Right, MetaLeft, VerbRight float64
			Acts, Whole                         int
		}
		raw := layoutOf(t, html, c.viewport, c.width, rowBoxes)
		if err := json.Unmarshal([]byte(raw), &box); err != nil {
			t.Fatalf("%v: %s", err, raw)
		}
		if box.Title < 120 || box.L1Right > box.MetaLeft || box.VerbRight > box.MetaLeft || box.Whole != box.Acts {
			t.Errorf("%dpx list in a %dpx window: %+v", c.width, c.viewport, box)
		}
		if c.viewport > 900 && box.Acts == 0 {
			t.Errorf("%dpx window: the row shows no action: %+v", c.viewport, box)
		}
	}
}
