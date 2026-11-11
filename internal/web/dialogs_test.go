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
	native := regexp.MustCompile(`(^|[^.\w])(confirm|prompt|alert)\(`)
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
