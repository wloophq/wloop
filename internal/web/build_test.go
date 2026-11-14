package web

import (
	"maps"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
)

func TestTheBuildChangesWithTheVersionAndAnyAsset(t *testing.T) {
	if first, again := Build("dev"), Build("dev"); first != again {
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
	delete(fewer, "app/app.js")
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
	if len(refs) < 4 {
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
