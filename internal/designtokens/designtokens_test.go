package designtokens

import (
	"strings"
	"testing"
)

const sample = `{
 "name": "acme",
 "color": {
  "themes": [{"id": "light", "name": "Light"}, {"id": "dark", "name": "Dark"}],
  "tokens": [
   {"name": "bg", "value": {"light": "#ffffff", "dark": "#000000"}, "usage": "Page."},
   {"name": "ink", "value": "#111111", "usage": "Text, same in both themes."}
  ]
 },
 "type": {
  "families": {"sans": "system-ui, sans-serif", "mono": "ui-monospace, monospace"},
  "groups": [
   {"name": "Text", "family": "sans", "styles": [{"name": "body", "fontSize": "13px"}, {"name": "row-title", "fontSize": "13px"}, {"name": "meta", "fontSize": "12px"}]},
   {"name": "Mono", "family": "mono", "styles": [{"name": "kpi", "fontSize": "20px"}, {"name": "code", "fontSize": "12px"}]}
  ]
 },
 "spacing": {"tokens": [{"name": "space-4", "value": "4px"}]},
 "radius": {"tokens": [{"name": "radius-md", "value": "4px"}]},
 "layout": {"tokens": [
  {"name": "rail-width", "value": "188px"},
  {"name": "breakpoint-narrow", "value": "900px"}
 ]},
 "meta": {"source": "github"}
}`

func TestCSSRendersThemesAndScales(t *testing.T) {
	tok, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	want := Begin + `
:root {
  --bg: #ffffff;
  --ink: #111111;
  --mono: ui-monospace, monospace;
  --sans: system-ui, sans-serif;
  color-scheme: light;
}

:root {
  --text-kpi: 20px;
  --text-body: 13px;
  --text-meta: 12px;
  --radius-md: 4px;
  --space-4: 4px;
  --rail-width: 188px;
}

@media (prefers-color-scheme: dark) {
  :root {
    --bg: #000000;
    color-scheme: dark;
  }
}
` + End
	if got := tok.CSS(); got != want {
		t.Errorf("CSS:\n%s\nwant:\n%s", got, want)
	}
}

const withFont = `"fonts": [{"family": "Geist", "src": "/static/fonts/Geist-Variable.woff2", "weight": "100 900"}],
  "families"`

func TestCSSDeclaresEachFontFace(t *testing.T) {
	tok, err := Parse([]byte(strings.Replace(sample, `"families"`, withFont, 1)))
	if err != nil {
		t.Fatal(err)
	}
	css := tok.CSS()
	for _, want := range []string{
		"@font-face {",
		`font-family: "Geist";`,
		`src: url("/static/fonts/Geist-Variable.woff2") format("woff2");`,
		"font-weight: 100 900;",
		"font-style: normal;",
		"font-display: swap;",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("CSS lacks %q:\n%s", want, css)
		}
	}
	if strings.Index(css, "@font-face") < strings.Index(css, Begin) {
		t.Error("the font face is outside the generated region")
	}
}

func TestParseRefusesAFontOutsideStatic(t *testing.T) {
	for _, src := range []string{"https://fonts.example/x.woff2", "/static/fonts/../app.js", "/static/fonts/x.ttf", `/static/fonts/x\".woff2`, "/static/fonts/x y.woff2"} {
		bad := strings.Replace(strings.Replace(sample, `"families"`, withFont, 1), "/static/fonts/Geist-Variable.woff2", src, 1)
		if _, err := Parse([]byte(bad)); err == nil || !strings.Contains(err.Error(), "Geist") {
			t.Errorf("%s: err = %v, want one naming the font", src, err)
		}
	}
}

const withMotion = `"motion": {"tokens": [{"name": "motion-fast", "value": "120ms"}, {"name": "motion-ease", "value": "cubic-bezier(0.2, 0, 0, 1)"}]},
 "meta": {`

func TestCSSRendersMotionAndReducedMotion(t *testing.T) {
	tok, err := Parse([]byte(strings.Replace(sample, `"meta": {`, withMotion, 1)))
	if err != nil {
		t.Fatal(err)
	}
	css := tok.CSS()
	for _, want := range []string{
		"  --motion-fast: 120ms;\n",
		"  --motion-ease: cubic-bezier(0.2, 0, 0, 1);\n",
		"@media (prefers-reduced-motion: reduce) {\n  :root {\n    --motion-fast: 0ms;\n  }\n}\n",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("CSS lacks %q:\n%s", want, css)
		}
	}
}

func TestParseRefusesAMotionDurationThatIsNotMilliseconds(t *testing.T) {
	bad := strings.Replace(strings.Replace(sample, `"meta": {`, withMotion, 1), `"120ms"`, `"0.12s"`, 1)
	if _, err := Parse([]byte(bad)); err == nil || !strings.Contains(err.Error(), "motion-fast") {
		t.Errorf("err = %v, want one naming motion-fast", err)
	}
}

func TestParseRefusesAStyleSizeThatIsNotPixels(t *testing.T) {
	bad := strings.Replace(sample, `"fontSize": "20px"`, `"fontSize": "1.5rem"`, 1)
	if _, err := Parse([]byte(bad)); err == nil || !strings.Contains(err.Error(), "kpi") {
		t.Errorf("err = %v, want one naming the kpi style", err)
	}
}

func TestParseRefusesASizeThatIsNotAPositiveNumber(t *testing.T) {
	for _, size := range []string{"NaNpx", "Infpx", "-12px", "0px", "0x1p4px"} {
		bad := strings.Replace(sample, `"fontSize": "20px"`, `"fontSize": "`+size+`"`, 1)
		if _, err := Parse([]byte(bad)); err == nil || !strings.Contains(err.Error(), "kpi") {
			t.Errorf("%s: err = %v, want one naming the kpi style", size, err)
		}
	}
}

func TestParseRefusesAColorWithoutALightValueOrWithAnUnknownTheme(t *testing.T) {
	for _, value := range []string{`{"dark": "#000000"}`, `{"light": "#ffffff", "Dark": "#000000"}`} {
		bad := strings.Replace(sample, `{"light": "#ffffff", "dark": "#000000"}`, value, 1)
		if _, err := Parse([]byte(bad)); err == nil || !strings.Contains(err.Error(), "bg") {
			t.Errorf("%s: err = %v, want one naming the bg color", value, err)
		}
	}
}

func TestParseRefusesThemesOtherThanLightThenDark(t *testing.T) {
	bad := strings.Replace(sample, `{"id": "light", "name": "Light"}, {"id": "dark", "name": "Dark"}`, `{"id": "dark", "name": "Dark"}, {"id": "light", "name": "Light"}`, 1)
	if _, err := Parse([]byte(bad)); err == nil {
		t.Error("parsed dark before light")
	}
}

func TestSpliceReplacesOnlyTheRegion(t *testing.T) {
	css := "a { }\n" + Begin + "\nold\n" + End + "\nb { }\n"
	got, err := Splice([]byte(css), Begin+"\nnew\n"+End)
	if err != nil {
		t.Fatal(err)
	}
	if want := "a { }\n" + Begin + "\nnew\n" + End + "\nb { }\n"; string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSpliceNeedsBothMarkers(t *testing.T) {
	for _, css := range []string{"a { }\n", Begin + "\na { }\n", "a { }\n" + End + "\n"} {
		if _, err := Splice([]byte(css), Begin+"\n"+End); err == nil {
			t.Errorf("spliced %q", css)
		}
	}
}

func TestBundleKeepsTheScaleAndTheRulesButNotTheColors(t *testing.T) {
	tok, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	app := "a { }\n" + Begin + "\nold\n" + End + "\n\nb { color: var(--ink); }\n"
	got, err := Bundle(tok, []byte(app), []byte(".proposed { }\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := BundleHeader + `
:root {
  --text-kpi: 20px;
  --text-body: 13px;
  --text-meta: 12px;
  --radius-md: 4px;
  --space-4: 4px;
  --rail-width: 188px;
}

b { color: var(--ink); }

.proposed { }
`
	if string(got) != want {
		t.Errorf("bundle:\n%s\nwant:\n%s", got, want)
	}
}
