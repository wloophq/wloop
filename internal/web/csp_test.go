package web

import (
	"net/http"
	"net/http/httptest"
	"regexp"
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

func TestTheCSPAllowsOnlyOwnFonts(t *testing.T) {
	h := Handler("")
	for _, p := range []string{"/", "/static/app.css", "/static/fonts/Geist-Variable.woff2", "/nowhere"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		csp := rec.Header().Get("Content-Security-Policy")
		if !strings.Contains(csp, "font-src 'self'") {
			t.Errorf("%s: CSP %q lacks font-src 'self'", p, csp)
		}
		for _, bad := range []string{"fonts.googleapis", "fonts.gstatic", "data:", "http"} {
			if strings.Contains(csp, bad) {
				t.Errorf("%s: CSP %q allows %s", p, csp, bad)
			}
		}
	}
}

var fontURL = regexp.MustCompile(`url\("([^"]+)"\) format\("woff2"\)`)

func TestEveryFontTheStylesheetNamesIsServed(t *testing.T) {
	_, faces, _ := strings.Cut(string(appCSS(t)), "@font-face")
	urls := fontURL.FindAllStringSubmatch(faces, -1)
	if len(urls) < 2 {
		t.Fatalf("app.css names %d fonts, want Geist and Geist Mono", len(urls))
	}
	h := Handler("")
	for _, u := range urls {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, u[1], nil))
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "font/woff2" {
			t.Errorf("%s: %d %q", u[1], rec.Code, rec.Header().Get("Content-Type"))
		}
		if !strings.HasPrefix(rec.Body.String(), "wOF2") {
			t.Errorf("%s is no woff2 file", u[1])
		}
	}
}

func TestTheFontsShipTheirLicence(t *testing.T) {
	b, err := files.ReadFile("static/fonts/OFL.txt")
	if err != nil || !strings.Contains(string(b), "SIL Open Font License") {
		t.Fatalf("static/fonts/OFL.txt: %v", err)
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

func TestThePageServesItsFavicon(t *testing.T) {
	h := Handler("")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(rec.Body.String(), `<link rel="icon" href="/static/favicon.svg?v=`) {
		t.Fatal("the page names no favicon")
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/favicon.svg", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/svg+xml" {
		t.Fatalf("favicon: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}
