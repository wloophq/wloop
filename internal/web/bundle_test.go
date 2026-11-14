package web

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/brand"
)

func servedBundle(t *testing.T) string {
	t.Helper()
	rec := httptest.NewRecorder()
	Handler("").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/app/app.js", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("the bundle: %d", rec.Code)
	}
	return rec.Body.String()
}

var dynamicCode = regexp.MustCompile(`\beval\s*\(|\bnew\s+Function\s*\(`)

func TestTheBundleRunsNoGeneratedCode(t *testing.T) {
	if m := dynamicCode.FindString(servedBundle(t)); m != "" {
		t.Errorf("the bundle calls %q, which script-src 'self' refuses", m)
	}
}

// placeholder is a brand or build placeholder the server replaces, as {{name}}; compiled code nests blocks ("{{")
// and the profile's words hold their own templates ({{.age}}), neither of which is one.
var placeholder = regexp.MustCompile(`\{\{\s*[A-Za-z_]+\s*\}\}`)

func TestTheBundleKeepsNoPlaceholder(t *testing.T) {
	js := servedBundle(t)
	if m := placeholder.FindString(js); m != "" {
		t.Errorf("the served bundle keeps the placeholder %s", m)
	}
	if !strings.Contains(js, brand.Name) {
		t.Errorf("the served bundle does not name %q: a brand placeholder did not survive the build", brand.Name)
	}
}
