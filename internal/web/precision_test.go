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
	still, _, _ := strings.Cut(css[strings.Index(css, "@media (prefers-reduced-motion: reduce)")+1:], "}")
	var durations, eases []string
	for _, m := range designTokens(t).Motion.Tokens {
		if strings.HasSuffix(m.Name, "-ease") {
			eases = append(eases, "var(--"+m.Name+")")
			continue
		}
		durations = append(durations, "var(--"+m.Name+")")
		if !regexp.MustCompile(`--` + m.Name + `:\s*0ms;`).MatchString(still) {
			t.Errorf("the token region does not set --%s to 0ms under prefers-reduced-motion: reduce", m.Name)
		}
	}
	reads := func(part string, tokens []string) bool {
		return slices.ContainsFunc(tokens, func(tok string) bool { return strings.Contains(part, tok) })
	}
	if strings.Contains(css, "@keyframes") || regexp.MustCompile(`(^|[;{\s])animation(-[a-z-]+)?\s*:`).MatchString(css) {
		t.Error("a stylesheet declares an animation; motion is a transition on a motion token, in answer to an action")
	}
	decl := regexp.MustCompile(`(?:^|;)\s*transition(-[a-z-]+)?\s*:([^;]*)`)
	moving := map[string]bool{}
	for _, r := range cssRules(css) {
		for _, m := range decl.FindAllStringSubmatch(r[1], -1) {
			for _, part := range strings.Split(m[2], ",") {
				if m[1] != "" || !reads(part, durations) || !reads(part, eases) {
					t.Errorf("%s: transition%s %q does not read a motion duration and a motion easing", r[0], m[1], strings.TrimSpace(part))
				}
			}
			moving[r[0]] = true
		}
	}
	// The rules that move answer an action: opening the detail or the palette, resolving a decision card,
	// and hover, focus or press on a control. The one exception is the float's peek, whose arrival and leave
	// announce a decision that just arrived, once per burst. Any other class live data toggles moves nothing.
	want := []string{"#detail", ".pal-back", ".d-gate, .d-draft", ".outbox .d-draft", ".find", ".views .nav", ".opt", ".tab", ".seg button", ".tools .pill",
		".fl-peek, .fl-flyout", `.fl-peek[data-state="leaving"], .fl-flyout[data-state="leaving"]`}
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
	for _, s := range []string{"#detail", ".pal-back", ".fl-peek, .fl-flyout"} {
		if !regexp.MustCompile(`@starting-style\s*\{\s*` + regexp.QuoteMeta(s) + `\s*\{`).MatchString(css) {
			t.Errorf("%s has no @starting-style to open from", s)
		}
	}
}

// The float's window is sized to what its page draws, so the page must not
// draw at the window's size: a root bounded by the viewport measures one
// pixel past it on each resize and grows the window a pixel a frame.
func TestTheFloatsRootSizesToItsContentNotToItsWindow(t *testing.T) {
	b, err := files.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range cssRules(string(b)) {
		if strings.TrimSpace(r[0]) == ".fl" {
			if !regexp.MustCompile(`(^|;)\s*width:\s*max-content\s*(;|$)`).MatchString(r[1]) {
				t.Fatalf(".fl is %q, want width: max-content", r[1])
			}
			return
		}
	}
	t.Fatal("no .fl rule")
}

// The pill sits at the float's anchor corner, which the host keeps still, so
// opening the panel grows the window away from the pill without moving it:
// at the bottom when the float opens upward from an edge, at the right when a
// free float opens leftward.
func TestThePillSitsAtTheFloatsAnchorCorner(t *testing.T) {
	b, err := files.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{".fl.up[data-edge]": false, ".fl.leftward:not([data-edge])": false}
	end := regexp.MustCompile(`(^|;)\s*align-items:\s*flex-end\s*(;|$)`)
	for _, r := range cssRules(string(b)) {
		if _, ok := want[strings.TrimSpace(r[0])]; ok && end.MatchString(r[1]) {
			want[strings.TrimSpace(r[0])] = true
		}
	}
	for sel, ok := range want {
		if !ok {
			t.Errorf("no %s { align-items: flex-end }", sel)
		}
	}
}
