package web

import (
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/brand"
)

func TestServesIndexAndAssets(t *testing.T) {
	h := Handler("")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `/static/app/app.js`) {
		t.Fatalf("%d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/static/app/app.js", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "EventSource") {
		t.Fatalf("%d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/nope", nil))
	if rec.Code != 404 {
		t.Fatalf("%d", rec.Code)
	}
}

func TestThePageNamesTheLinkScheme(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler("").ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if want := `<meta name="link-scheme" content="` + brand.URLScheme() + `">`; !strings.Contains(rec.Body.String(), want) {
		t.Fatalf("the page lacks %s", want)
	}
}

func TestHiddenBeatsEveryDisplayRule(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler("").ServeHTTP(rec, httptest.NewRequest("GET", "/static/app.css", nil))
	if !strings.Contains(rec.Body.String(), "[hidden] { display: none !important; }") {
		t.Fatal("app.css lets a display rule show an element marked hidden")
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
