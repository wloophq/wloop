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
  "groups": [{"name": "Text", "family": "sans", "styles": [{"name": "body", "fontSize": "13px"}]}]
 },
 "text": {"tokens": [{"name": "text-body", "value": "13px"}]},
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
  --text-body: 13px;
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

func TestParseRefusesAStyleOffTheTextScale(t *testing.T) {
	bad := strings.Replace(sample, `"fontSize": "13px"`, `"fontSize": "17px"`, 1)
	if _, err := Parse([]byte(bad)); err == nil || !strings.Contains(err.Error(), "body") {
		t.Errorf("err = %v, want one naming the body style", err)
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
  --text-body: 13px;
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
