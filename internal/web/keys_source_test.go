package web

import (
	"regexp"
	"slices"
	"testing"
)

func TestSourceLinkOpensOnlyHTTPS(t *testing.T) {
	src := staticJS(t, "keys.js")
	script := jsFunction(t, appJS(t), "safeURL") + jsFunction(t, src, "sourceLink")
	for _, name := range []string{"isTask", "sameUnit"} {
		script += regexp.MustCompile(`(?m)^const `+name+` = .*;$`).FindString(src) + "\n"
	}
	u := map[string]any{"provider": "slack", "key": "C1/1.1"}
	task := func(link string) map[string]any {
		return map[string]any{"id": "e1", "units": []any{u}, "cards": []any{map[string]any{"card": map[string]any{"unit": u, "link": link}}}}
	}
	in := []any{
		map[string]any{"link": "javascript:alert(1)"},
		map[string]any{"link": "https://github.com/acme/api/pull/1"},
		task("data:text/html,x"),
		task("https://acme.slack.com/archives/C1/p1"),
	}
	var got []string
	nodeScript(t, script+`
process.stdout.write(JSON.stringify(input.map(sourceLink)));`, in, &got)
	if want := []string{"", "https://github.com/acme/api/pull/1", "", "https://acme.slack.com/archives/C1/p1"}; !slices.Equal(got, want) {
		t.Fatalf("%q, want %q", got, want)
	}
}
