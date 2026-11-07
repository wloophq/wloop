package web

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// layoutOf renders rows, the HTML of list rows, in a page of app.css width px wide, and returns what probe,
// a script over the page, writes.
func layoutOf(t *testing.T, rows string, width int, probe string) string {
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

// TestARowsFirstLineKeepsRoomBesideAWideMeta lays out a row whose meta holds many pieces and actions: its
// first line keeps room for its situation and title, and nothing of it paints under the meta.
func TestARowsFirstLineKeepsRoomBesideAWideMeta(t *testing.T) {
	rows := lensRows()
	pr := rows[1]
	pr["kind"] = "no_reviewer"
	pr["title"] = "Add retries to the uploader so flaky networks stop failing the whole batch"
	pr["brief"] = map[string]any{"risk": "medium", "size": "s", "kind": "fix", "tags": []string{"lang:go", "lang:sql", "change:fix", "domain:billing", "area:uploads", "team:platform"}}
	pr["views"] = []string{"mine"}
	actions := []map[string]any{}
	specs := map[string]any{}
	for _, id := range []string{"request-review", "pause", "dismiss", "take_over", "open"} {
		actions = append(actions, map[string]any{"id": id, "enabled": true})
		specs[id] = map[string]any{"id": id, "label": "Action " + id, "group": "control", "surfaces": []string{"row"}}
	}
	pr["actions"] = actions
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	var got struct{ HTML string }
	rowViewRun(t, []byte(servedCatalog(t)), `state.rows = [input.pr];
state.specs = { version: "", byId: input.specs };
const lens = lensOf({ ...readHash(), view: "mine" });
const esc = (s) => String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/"/g, "&quot;");
const ser = (n) => n instanceof Text ? esc(n.data) : "<" + n.tag + Object.entries(n.attrs).map(([k, v]) => " " + k + "=\"" + esc(v) + "\"").join("") + ">" + n.children.map(ser).join("") + "</" + n.tag + ">";
process.stdout.write(JSON.stringify({ HTML: ser(rowView(rowModel(input.pr, "", lens, ""), lens)) }));`,
		map[string]any{"pr": wireRow("me", now, pr), "specs": specs, "now": now.UnixMilli()}, &got)
	var box struct{ Title, L1Right, MetaLeft, VerbRight float64 }
	raw := layoutOf(t, got.HTML, 480, `
const r = (sel) => document.querySelector(sel).getBoundingClientRect();
return JSON.stringify({ Title: r(".row .t").width, L1Right: r(".row .l1").right, MetaLeft: r(".row > .meta").left, VerbRight: r(".row .l1 .verb").right });`)
	if err := json.Unmarshal([]byte(raw), &box); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	if box.Title < 120 || box.L1Right > box.MetaLeft || box.VerbRight > box.MetaLeft {
		t.Fatalf("%+v: the first line keeps room for its title and stays left of the meta", box)
	}
}
