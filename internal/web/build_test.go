package web

import (
	"maps"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestTheBuildChangesWithTheVersionAndAnyAsset(t *testing.T) {
	if Build("dev") != Build("dev") {
		t.Fatal("the build of one binary moves")
	}
	if Build("v1.0.0") == Build("v1.0.1") {
		t.Error("two versions share a build")
	}
	for name, a := range assets() {
		changed := maps.Clone(assets())
		changed[name] = asset{body: append([]byte("x"), a.body...), etag: a.etag + "x"}
		if buildOf("dev", changed) == Build("dev") {
			t.Errorf("a dev build keeps its id when %s changes", name)
		}
	}
	fewer := maps.Clone(assets())
	delete(fewer, "app.js")
	if buildOf("dev", fewer) == Build("dev") {
		t.Error("a dev build keeps its id when an asset goes")
	}
}

func TestThePageNamesItsBuildAndRevalidatesItsAssets(t *testing.T) {
	h := Handler("v9")
	build := Build("v9")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	page := rec.Body.String()
	if !regexp.MustCompile(`<meta name="build" content="` + build + `">`).MatchString(page) {
		t.Fatalf("the page does not name build %s", build)
	}
	if rec.Header().Get("Cache-Control") != "no-cache" || rec.Header().Get("ETag") == "" {
		t.Errorf("page headers %v", rec.Header())
	}
	refs := regexp.MustCompile(`(?:src|href)="(/static/[^"]+)"`).FindAllStringSubmatch(page, -1)
	if len(refs) < 5 {
		t.Fatalf("assets %v", refs)
	}
	for _, m := range refs {
		if !regexp.MustCompile(`\?v=` + build + `$`).MatchString(m[1]) {
			t.Errorf("%s is not under the build's URL", m[1])
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, m[1], nil))
		etag := rec.Header().Get("ETag")
		if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-cache" || etag == "" {
			t.Errorf("%s: %d %v", m[1], rec.Code, rec.Header())
			continue
		}
		req := httptest.NewRequest(http.MethodGet, m[1], nil)
		req.Header.Set("If-None-Match", etag)
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotModified {
			t.Errorf("%s revalidated with its ETag: %d", m[1], rec.Code)
		}
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/index.html", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("the page under /static/: %d", rec.Code)
	}
}

func TestANewBuildReloadsOrOffersTo(t *testing.T) {
	type outcome struct {
		Reloads int    `json:"reloads"`
		Banner  bool   `json:"banner"`
		Stored  string `json:"stored"`
	}
	cases := []struct {
		name   string
		served string
		setup  string
		want   outcome
	}{
		{"same build", "b1", "", outcome{Banner: false}},
		{"no build named", "", "", outcome{}},
		{"a new build and nothing held", "b2", "", outcome{Reloads: 1, Stored: "b2"}},
		{"text in a composer", "b2", `fields.push({ id: "", value: "half a reply" });`, outcome{Banner: true}},
		{"text in the filter only", "b2", `fields.push({ id: "search", value: "repo:acme" });`, outcome{Reloads: 1, Stored: "b2"}},
		{"an open dialog", "b2", `dialogOpen = true;`, outcome{Banner: true}},
		{"guidance kept for a closed detail", "b2", `state.guidance["github:acme/api#1"] = "rebase first";`, outcome{Banner: true}},
		{"a draft being edited", "b2", `detailState.editing.set("draft:7", "Deployed");`, outcome{Banner: true}},
		{"an answer typed", "b2", `detailState.answer = "after ten";`, outcome{Banner: true}},
		{"an action on its way", "b2", `state.busy.add("github:acme/api#1");`, outcome{Banner: true}},
		{"reloaded for this build already", "b2", `stored = "b2";`, outcome{Banner: true, Stored: "b2"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got outcome
			nodeScript(t, `
const fields = [];
let dialogOpen = false;
let stored = "";
let reloads = 0;
const banner = { hidden: true };
const reload = { onclick: null };
const state = { guidance: {}, busy: new Set() };
const detailState = { answer: "", guidance: "", compose: { key: "", text: "", reaction: "" }, editing: new Map() };
const document = {
  querySelector: (sel) => sel.startsWith("meta") ? { getAttribute: () => "b1" } : dialogOpen ? {} : null,
  querySelectorAll: () => fields,
  getElementById: (id) => id === "update" ? banner : id === "update-reload" ? reload : null,
};
const sessionStorage = { getItem: () => stored, setItem: (k, v) => { stored = v; } };
const location = { reload: () => { reloads++; } };
`+staticJS(t, "update.js")+c.setup+`
checkBuild({ build: input });
if (!banner.hidden) reload.onclick();
process.stdout.write(JSON.stringify({ reloads: banner.hidden ? reloads : reloads - 1, banner: !banner.hidden, stored }));`, c.served, &got)
			if got != c.want {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestTheCockpitChecksTheBuildOnEveryHealth(t *testing.T) {
	src := appJS(t)
	for _, want := range []string{"state.health = health.value;\n    checkBuild(state.health);", "state.health = await getJSON(\"/api/health\");\n      checkBuild(state.health);"} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
}
