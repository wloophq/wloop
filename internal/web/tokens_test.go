package web

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/designtokens"
)

func designTokens(t *testing.T) designtokens.Tokens {
	t.Helper()
	raw, err := os.ReadFile("../../design/tokens.json")
	if err != nil {
		t.Fatal(err)
	}
	tok, err := designtokens.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func appCSS(t *testing.T) []byte {
	t.Helper()
	css, err := files.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	return css
}

func TestAppCSSTokensFollowDesign(t *testing.T) {
	css := appCSS(t)
	want, err := designtokens.Splice(css, designTokens(t).CSS())
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != string(css) {
		t.Error("static/app.css's token block differs from design/tokens.json; run `go generate ./internal/web`")
	}
}

var maxWidthQuery = regexp.MustCompile(`@media \(max-width: ([^)]+)\)`)

func TestAppCSSBreakpointsFollowDesign(t *testing.T) {
	breakpoints := map[string]bool{}
	for _, l := range designTokens(t).Layout.Tokens {
		if strings.HasPrefix(l.Name, "breakpoint-") {
			breakpoints[l.Value] = true
		}
	}
	queries := maxWidthQuery.FindAllStringSubmatch(string(appCSS(t)), -1)
	if len(queries) == 0 {
		t.Fatal("no max-width query in app.css")
	}
	for _, q := range queries {
		if !breakpoints[q[1]] {
			t.Errorf("app.css queries max-width %s, which is no breakpoint in design/tokens.json", q[1])
		}
	}
}

var (
	propertyUse  = regexp.MustCompile(`var\((--[a-z0-9-]+)\)`)
	propertyDecl = regexp.MustCompile(`(--[a-z0-9-]+):`)
)

func TestEveryPropertyAStylesheetReadsIsDeclared(t *testing.T) {
	bundle, err := os.ReadFile("../../design/components/bundle.css")
	if err != nil {
		t.Fatal(err)
	}
	app := string(appCSS(t))
	for name, css := range map[string]string{"static/app.css": app, "design/components/bundle.css": app + string(bundle)} {
		// The design system's page declares the font properties the bundle's
		// families read.
		declared := map[string]bool{"--font-mono": name != "static/app.css", "--font-sans": name != "static/app.css"}
		for _, m := range propertyDecl.FindAllStringSubmatch(css, -1) {
			declared[m[1]] = true
		}
		for _, m := range propertyUse.FindAllStringSubmatch(css, -1) {
			if !declared[m[1]] {
				t.Errorf("%s reads %s, which nothing declares", name, m[1])
				declared[m[1]] = true
			}
		}
	}
}

func TestDesignBundleFollowsAppCSS(t *testing.T) {
	t.Skip("K1b")
	preview, err := os.ReadFile("../../design/components/preview.css")
	if err != nil {
		t.Fatal(err)
	}
	want, err := designtokens.Bundle(designTokens(t), appCSS(t), preview)
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
