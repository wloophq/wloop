package web

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// The native window's WebKit shows no JavaScript dialog: confirm and prompt answer nothing there, so a page
// that asks through them backs out without a word.
func TestThePagesAskNothingThroughBrowserDialogs(t *testing.T) {
	native := regexp.MustCompile(`(^|[^.\w$]|\b(window|globalThis|self)\.)(confirm|prompt|alert)\s*\(`)
	for line, asks := range map[string]bool{
		`if (!confirm("Remove?")) return;`: true, `window.confirm("Remove?")`: true, `globalThis.prompt ("Days?")`: true, `alert("x")`: true,
		`await askConfirm("Remove?")`: false, `env.confirm(text)`: false, `askInput({ label })`: false,
	} {
		if native.MatchString(line) != asks {
			t.Fatalf("the check reads %q as asking through a browser dialog: %v, want %v", line, !asks, asks)
		}
	}
	names, err := fs.Glob(files, "static/*.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		for i, line := range strings.Split(staticJS(t, strings.TrimPrefix(name, "static/")), "\n") {
			if native.MatchString(line) && !strings.HasPrefix(strings.TrimSpace(line), "//") {
				t.Errorf("%s:%d asks through a browser dialog: %s", name, i+1, strings.TrimSpace(line))
			}
		}
	}
}
