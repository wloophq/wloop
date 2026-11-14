package web

import (
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// cssRules are the rules of css as selector and body, the rules nested in an at-rule among them.
func cssRules(css string) [][2]string {
	css = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(css, "")
	var out [][2]string
	for _, m := range regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`).FindAllStringSubmatch(css, -1) {
		out = append(out, [2]string{strings.TrimSpace(m[1][strings.LastIndex(m[1], ";")+1:]), m[2]})
	}
	return out
}

// motionCSS is the cockpit's stylesheet.
func motionCSS(t *testing.T) string {
	t.Helper()
	app, err := fs.ReadFile(files, "static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	return string(app)
}

func TestMotionStopsUnderReducedMotion(t *testing.T) {
	css := motionCSS(t)
	if !regexp.MustCompile(`@media \(prefers-reduced-motion: reduce\)\s*\{\s*:root\s*\{\s*--motion-fast:\s*0ms;\s*\}`).MatchString(css) {
		t.Error("the token region does not set --motion-fast to 0ms under prefers-reduced-motion: reduce")
	}
	if strings.Contains(css, "@keyframes") || regexp.MustCompile(`(^|[;{\s])animation(-[a-z-]+)?\s*:`).MatchString(css) {
		t.Error("a stylesheet declares an animation; motion is a transition on the motion token, in answer to an action")
	}
	decl := regexp.MustCompile(`(?:^|;)\s*transition(-[a-z-]+)?\s*:([^;]*)`)
	moving := map[string]bool{}
	for _, r := range cssRules(css) {
		for _, m := range decl.FindAllStringSubmatch(r[1], -1) {
			for _, part := range strings.Split(m[2], ",") {
				if m[1] != "" || !strings.Contains(part, "var(--motion-fast)") || !strings.Contains(part, "var(--motion-ease)") {
					t.Errorf("%s: transition%s %q does not read var(--motion-fast) var(--motion-ease)", r[0], m[1], strings.TrimSpace(part))
				}
			}
			moving[r[0]] = true
		}
	}
	// The rules that move answer an action: opening the detail or the palette, resolving a decision card,
	// and hover, focus or press on a control. A class live data toggles moves nothing.
	want := []string{"#detail", ".pal-back", ".d-gate, .d-draft", ".outbox .d-draft", ".find", ".views .nav", ".opt", ".tab", ".seg button", ".tools .pill"}
	var got []string
	for s := range moving {
		got = append(got, s)
	}
	for _, s := range want {
		if !moving[s] {
			t.Errorf("%s has no transition on the motion token", s)
		}
	}
	for _, s := range got {
		if !slices.Contains(want, s) && !strings.HasPrefix(s, ".act") {
			t.Errorf("%s moves: only an action's answer moves, add it here once it answers one", s)
		}
	}
	for _, s := range []string{"#detail", ".pal-back"} {
		if !regexp.MustCompile(`@starting-style\s*\{\s*` + regexp.QuoteMeta(s) + `\s*\{`).MatchString(css) {
			t.Errorf("%s has no @starting-style to open from", s)
		}
	}
}
