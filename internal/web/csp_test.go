package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEveryCockpitResponseCarriesTheCSP(t *testing.T) {
	h := Handler("")
	for _, p := range []string{"/", "/static/app.js", "/static/app.css", "/static/vendor/echarts.min.js", "/nowhere"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		csp := rec.Header().Get("Content-Security-Policy")
		for _, want := range []string{"script-src 'self'", "object-src 'none'", "frame-ancestors 'none'", "base-uri 'none'"} {
			if !strings.Contains(csp, want) {
				t.Errorf("%s: CSP %q lacks %q", p, csp, want)
			}
		}
		if strings.Contains(csp, "unsafe") {
			t.Errorf("%s: CSP %q", p, csp)
		}
		if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
			t.Errorf("%s: X-Frame-Options %q", p, got)
		}
	}
}

func TestThePageHasNoInlineScript(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler("").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	page := rec.Body.String()
	for _, tag := range strings.Split(page, "<script")[1:] {
		head, body, _ := strings.Cut(tag, ">")
		if !strings.Contains(head, `src="/static/`) || !strings.HasPrefix(body, "</script>") {
			t.Errorf("inline or external script: <script%s", tag[:min(len(tag), 80)])
		}
	}
	if strings.Contains(page, " onclick=") || strings.Contains(page, "javascript:") {
		t.Error("inline handler")
	}
}
