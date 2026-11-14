package web

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestCockpitClassesSayTask(t *testing.T) {
	old := `(gtd|entry|entries)\b`
	patterns := map[string]*regexp.Regexp{
		"static/app.css": regexp.MustCompile(`\.([\w-]*-)?` + old),
	}
	err := filepath.WalkDir("ui/src", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".svelte") {
			patterns[p] = regexp.MustCompile(`class=\{?["[][^>]*\b` + old)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
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
