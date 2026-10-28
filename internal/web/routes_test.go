package web

import (
	"strings"
	"testing"
)

func routesPrelude(t *testing.T) string {
	src := appJS(t)
	var b strings.Builder
	b.WriteString(catalogPrelude(t))
	b.WriteString(noPlugins)
	b.WriteString(`const location = { hash: "" }; const history = { replaceState: (_, __, h) => { location.hash = h; } };` + "\n")
	b.WriteString(`const window = { addEventListener() {} };` + "\n")
	b.WriteString(staticJS(t, "routes.js"))
	b.WriteString(staticJS(t, "views.js"))
	for _, c := range []string{"UNIT_KEY", "QUERY_MAX"} {
		b.WriteString(jsConst(t, src, c))
	}
	for _, f := range []string{"fieldById", "tagKeyField", "groupField", "parseSort", "readHash", "hashString"} {
		b.WriteString(jsFunction(t, src, f))
	}
	return b.String()
}

func TestTaskRouteOverView(t *testing.T) {
	var got struct {
		View, Entry, PR string
		Detail          bool
		List            string
		Back            string
	}
	nodeRun(t, routesPrelude(t)+treeJS+staticJS(t, "detail.js"), nil, `
gtdState.catalog = input;
gtdState.key = gtdQueryKey("next", "", []);
gtdState.entries = [{ id: "e-1", state: "next", title: "Write the release notes", contexts: [], units: [] }];
location.hash = "#view=next&entry=e-1";
const r = readHash();
const box = { dataset: {}, hidden: true, replaceChildren() {}, contains: () => false };
globalThis.document = { activeElement: null, body: { classList: { add() {} } } };
const shown = renderEntryDetail(box, r);
process.stdout.write(JSON.stringify({ View: r.view, Entry: r.entry, PR: r.pr, Detail: shown && !box.hidden, List: text(gtdList(r, r.entry)), Back: hashString({ ...r, entry: "" }) }));`, viewsCatalog(), &got)
	if got.View != "next" || got.Entry != "e-1" || got.PR != "" {
		t.Errorf("#view=next&entry=e-1 reads view %q entry %q unit %q", got.View, got.Entry, got.PR)
	}
	if !got.Detail || !strings.Contains(got.List, "Write the release notes") {
		t.Errorf("detail open %v, list %q", got.Detail, got.List)
	}
	if got.Back != "view=next" {
		t.Errorf("closing the entry leaves %q, want view=next", got.Back)
	}
}

func TestFirstLoadResolvesACoreViewOnceItsCatalogIsIn(t *testing.T) {
	var got struct{ Asked []string }
	nodeRun(t, routesPrelude(t), nil, `
const asked = [];
async function getJSON(u) { asked.push(u); return u === "/api/gtd" ? input : { epoch: "e", rev: 1, full: true, entries: [], removed: [], counters: { views: {} } }; }
location.hash = "#view=focus";
(async () => {
  await loadGTD();
  process.stdout.write(JSON.stringify({ Asked: asked }));
})();`, viewsCatalog(), &got)
	if want := "/api/gtd,/api/entries?view=focus"; strings.Join(got.Asked, ",") != want {
		t.Errorf("first load asked %v, want %s", got.Asked, want)
	}
}
