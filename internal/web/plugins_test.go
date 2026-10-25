package web

import (
	"encoding/json"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
)

// noPlugins stands in for plugins.js where a test runs app.js's functions
// alone.
const noPlugins = "const pluginViews = new Map();\nconst applyPlugins = () => {};\nconst providerName = (id) => id;\nconst providerReacts = () => false;\n"

func pluginsJS(t *testing.T) string {
	t.Helper()
	raw, err := fs.ReadFile(files, "static/plugins.js")
	if err != nil {
		t.Fatal(err)
	}
	return string(render(raw))
}

// nodePlugins runs plugins.js, then main, which writes JSON to stdout.
func nodePlugins(t *testing.T, prelude, main string, in, out any) {
	t.Helper()
	nodeRun(t, prelude+pluginsJS(t), nil, main, in, out)
}

const fakeView = `export function mount(el, ctx) {
  const log = globalThis.viewLog;
  log.push("mount");
  globalThis.ctxKeys = Object.keys(ctx).sort().concat(Object.keys(ctx.keys).map((k) => "keys." + k));
  ctx.subscribe((rows) => log.push("rows:" + rows.length));
  ctx.keys.register("x", "Do x", () => log.push("key:x"));
  ctx.select({ unit: { provider: "fake", key: "T1/C1" } });
  ctx.act({ provider: "fake", key: "T1/C1" }, "nudge", { guidance: "hi" }).then((ok) => log.push("acted:" + ok));
  ctx.list(el, [{ label: "r" }], { empty: "none" });
  ctx.detail(el, { provider: "fake", key: "T1/C1" });
  log.push("theme:" + typeof ctx.theme);
}
export function unmount() { globalThis.viewLog.push("unmount"); }
`

func fakeViewPlugins(enabled bool) []api.Plugin {
	p := api.Plugin{ID: "fake", Enabled: enabled}
	if enabled {
		p.UI = &api.PluginUI{Web: &api.WebView{Module: "view.js", Views: []string{"fake-board"}}}
	}
	return []api.Plugin{{ID: "github", Enabled: true}, p}
}

func TestPluginViewMountsAndUnmounts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "view.mjs")
	if err := os.WriteFile(path, []byte(fakeView), 0o644); err != nil {
		t.Fatal(err)
	}
	in := map[string]any{
		"view":     (&url.URL{Scheme: "file", Path: path}).String(),
		"enabled":  fakeViewPlugins(true),
		"disabled": fakeViewPlugins(false),
	}
	var got struct {
		Log, AfterUnmount, Raced, Disabled, URLs, CtxKeys []string
		Handled, HandledAfter                             bool
		Cleared                                           int
	}
	nodePlugins(t, "", `(async () => {
  const log = globalThis.viewLog = [];
  const urls = [];
  const load = (u) => { urls.push(u); return import(input.view); };
  const el = { cleared: 0, replaceChildren() { this.cleared++; } };
  let rows = [{ id: 1 }];
  const host = {
    rows: () => rows,
    select: (x) => log.push("select:" + x.unit.key),
    act: async (u, a, i) => { log.push("act:" + u.key + ":" + a + ":" + i.guidance); return true; },
    list: (_, rs, o) => log.push("list:" + rs.length + ":" + o.empty),
    detail: (_, u) => log.push("detail:" + u.key),
  };
  applyPlugins(input.enabled);
  await showPluginView("fake-board", el, host, load);
  await showPluginView("fake-board", el, host, load);
  await new Promise((r) => setTimeout(r, 0));
  publishPlugins(rows = [{ id: 1 }, { id: 2 }]);
  publishPlugins(rows);
  const handled = pluginKeyDown({ key: "x" });
  unmountPluginView();
  const cleared = el.cleared;
  const before = log.length;
  publishPlugins([{ id: 1 }, { id: 2 }, { id: 3 }]);
  const handledAfter = pluginKeyDown({ key: "x" });
  const afterUnmount = log.slice(before);

  const racing = showPluginView("fake-board", el, host, load);
  unmountPluginView();
  await racing;
  const raced = log.slice(before);

  applyPlugins(input.disabled);
  const loads = urls.length;
  await showPluginView("fake-board", el, host, load);
  const disabled = urls.slice(loads);
  process.stdout.write(JSON.stringify({ Log: log.slice(0, before), AfterUnmount: afterUnmount, Raced: raced, Disabled: disabled, URLs: urls, CtxKeys: globalThis.ctxKeys, Handled: handled, HandledAfter: handledAfter, Cleared: cleared }));
})();`, in, &got)

	acted := slices.Index(got.Log, "acted:true")
	if acted < 0 {
		t.Fatalf("act never resolved: %v", got.Log)
	}
	log := slices.Delete(slices.Clone(got.Log), acted, acted+1)
	want := []string{"mount", "rows:1", "select:T1/C1", "act:T1/C1:nudge:hi", "list:1:none", "detail:T1/C1", "theme:function", "rows:2", "key:x", "unmount"}
	if !slices.Equal(log, want) {
		t.Errorf("log %v, want %v", log, want)
	}
	if !slices.Equal(got.CtxKeys, []string{"act", "detail", "keys", "list", "select", "subscribe", "theme", "keys.register"}) {
		t.Errorf("ctx %v", got.CtxKeys)
	}
	if !got.Handled || got.HandledAfter || len(got.AfterUnmount) != 0 || got.Cleared == 0 {
		t.Errorf("after unmount: handled %v then %v, log %v, cleared %d", got.Handled, got.HandledAfter, got.AfterUnmount, got.Cleared)
	}
	if len(got.Raced) != 0 {
		t.Errorf("a view left during its import mounted: %v", got.Raced)
	}
	if !slices.Equal(got.URLs, []string{"/plugins/fake/view.js", "/plugins/fake/view.js"}) || len(got.Disabled) != 0 {
		t.Errorf("imports %v, while disabled %v", got.URLs, got.Disabled)
	}
}

var customProp = regexp.MustCompile(`--([\w-]+):\s*([^;]+);`)

func TestPluginThemeFollowsCockpit(t *testing.T) {
	b, err := files.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	lightCSS, darkCSS, _ := strings.Cut(string(b), "@media (prefers-color-scheme: dark)")
	lightCSS, _, _ = strings.Cut(lightCSS, "\n}\n")
	darkCSS, _, _ = strings.Cut(darkCSS, "\n}\n")
	want := map[string]map[string]string{"light": {}, "dark": {}}
	for _, m := range customProp.FindAllStringSubmatch(lightCSS, -1) {
		want["light"][m[1]], want["dark"][m[1]] = m[2], m[2]
	}
	for _, m := range customProp.FindAllStringSubmatch(darkCSS, -1) {
		want["dark"][m[1]] = m[2]
	}
	if len(want["light"]) < 20 {
		t.Fatalf("read only %d tokens from app.css", len(want["light"]))
	}
	var got map[string]struct {
		Mode   string            `json:"mode"`
		Tokens map[string]string `json:"tokens"`
	}
	nodePlugins(t, `let mode = "light";
const document = { documentElement: {} };
const matchMedia = (q) => ({ matches: q === "(prefers-color-scheme: dark)" && mode === "dark" });
`, `const getComputedStyle = () => ({ getPropertyValue: (name) => " " + (input[mode][name.slice(2)] || "") });
const out = {};
for (mode of ["light", "dark"]) out[mode] = pluginTheme();
process.stdout.write(JSON.stringify(out));`, want, &got)
	for _, mode := range []string{"light", "dark"} {
		if got[mode].Mode != mode {
			t.Errorf("%s: mode %q", mode, got[mode].Mode)
		}
		for name, v := range want[mode] {
			if got[mode].Tokens[name] != v {
				t.Errorf("%s --%s: %q, want %q", mode, name, got[mode].Tokens[name], v)
			}
		}
		for name := range got[mode].Tokens {
			if _, ok := want[mode][name]; !ok {
				t.Errorf("%s: --%s is not in app.css", mode, name)
			}
		}
	}
	raw, _ := json.Marshal(got)
	t.Logf("%s", raw)
}
