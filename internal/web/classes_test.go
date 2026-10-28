package web

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestCockpitClassesSayTask(t *testing.T) {
	t.Skip("carried from R2 review: K1a renames the gtd and entry CSS classes")
	old := `(gtd|entry|entries)\b`
	patterns := map[string]*regexp.Regexp{
		"static/app.css":    regexp.MustCompile(`\.([\w-]*-)?` + old),
		"static/index.html": regexp.MustCompile(`class="[^"]*\b` + old),
	}
	js, err := filepath.Glob("static/*.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range js {
		patterns[f] = regexp.MustCompile(`class: "[^"]*\b` + old)
	}
	for f, re := range patterns {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllString(string(b), -1) {
			t.Errorf("%s: %s", f, m)
		}
	}
}
