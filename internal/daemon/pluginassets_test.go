package daemon

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/wloophq/wloop/internal/domain/unit"
)

const fakePlugin unit.ProviderID = "fake"

var fakeAssets = fstest.MapFS{
	"view.js":       {Data: []byte("export function mount(el, ctx) {}\nexport function unmount() {}\n")},
	"css/view.css":  {Data: []byte(".x{}")},
	"../escape.txt": {Data: []byte("no")},
}

func assetServer(t *testing.T, enabled bool) *Server {
	return newServerWith(t, func(d *Deps) {
		d.Runtimes = []PluginRuntime{{ID: fakePlugin, WebAssets: fakeAssets}}
		d.PluginEnabled = func(p unit.ProviderID) bool { return p != fakePlugin || enabled }
	})
}

func getAsset(s *Server, path string, auth bool) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = "localhost:47000"
	if auth {
		s.token = "a-token-long-enough-for-the-check"
		req.AddCookie(&http.Cookie{Name: tokenCookie, Value: s.token})
		s.AuthHandler().ServeHTTP(rec, req)
		return rec
	}
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func TestPluginAssetsOnlyWhenEnabled(t *testing.T) {
	t.Parallel()
	on := assetServer(t, true)
	rec := getAsset(on, "/plugins/fake/view.js", false)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "export function mount") {
		t.Fatalf("enabled: %d %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Fatalf("content type %q", ct)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("no nosniff: %v", rec.Header())
	}
	if rec := getAsset(on, "/plugins/fake/css/view.css", false); rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/css") {
		t.Fatalf("nested asset: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if rec := getAsset(on, "/plugins/fake/view.js", true); rec.Code != http.StatusOK {
		t.Fatalf("over the token port: %d", rec.Code)
	}
	for _, path := range []string{"/plugins/fake/missing.js", "/plugins/fake/", "/plugins/other/view.js", "/plugins/fake/..%2fescape.txt", "/plugins/fake/css/..%2f..%2fescape.txt"} {
		if rec := getAsset(on, path, false); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d %s", path, rec.Code, rec.Body)
		}
	}

	off := assetServer(t, false)
	for _, path := range []string{"/plugins/fake/view.js", "/plugins/fake/css/view.css"} {
		if rec := getAsset(off, path, false); rec.Code != http.StatusNotFound {
			t.Errorf("disabled %s: %d %s", path, rec.Code, rec.Body)
		}
	}
}
