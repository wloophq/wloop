package web

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/designtokens"
)

func TestAppCSSTokensFollowDesign(t *testing.T) {
	raw, err := os.ReadFile("../../design/tokens.json")
	if err != nil {
		t.Fatal(err)
	}
	tok, err := designtokens.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	css, err := files.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	want, err := designtokens.Splice(css, tok.CSS())
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != string(css) {
		t.Error("static/app.css's token block differs from design/tokens.json; run `go generate ./internal/web`")
	}
}

var maxWidthQuery = regexp.MustCompile(`@media \(max-width: ([^)]+)\)`)

func TestAppCSSBreakpointsFollowDesign(t *testing.T) {
	raw, err := os.ReadFile("../../design/tokens.json")
	if err != nil {
		t.Fatal(err)
	}
	tok, err := designtokens.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	breakpoints := map[string]bool{}
	for _, l := range tok.Layout.Tokens {
		if strings.HasPrefix(l.Name, "breakpoint-") {
			breakpoints[l.Value] = true
		}
	}
	css, err := files.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	queries := maxWidthQuery.FindAllStringSubmatch(string(css), -1)
	if len(queries) == 0 {
		t.Fatal("no max-width query in app.css")
	}
	for _, q := range queries {
		if !breakpoints[q[1]] {
			t.Errorf("app.css queries max-width %s, which is no breakpoint in design/tokens.json", q[1])
		}
	}
}

func TestDesignBundleFollowsAppCSS(t *testing.T) {
	raw, err := os.ReadFile("../../design/tokens.json")
	if err != nil {
		t.Fatal(err)
	}
	tok, err := designtokens.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	css, err := files.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	preview, err := os.ReadFile("../../design/components/preview.css")
	if err != nil {
		t.Fatal(err)
	}
	want, err := designtokens.Bundle(tok, css, preview)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("../../design/components/bundle.css")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Error("design/components/bundle.css differs from app.css and preview.css; run `go generate ./internal/web`")
	}
}
