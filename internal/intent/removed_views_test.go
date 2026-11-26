package intent

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// removedViews are core views removed with no alias; what replaces each is in the release notes.
var removedViews = []string{"needs-me", "blocked-by-me", "home-blocked"}

// TestNothingLinksToARemovedView checks that the bundled profiles, the cockpit, the menu bar and the CLI name no
// removed view: no route, view table, view reference or key binding to one.
func TestNothingLinksToARemovedView(t *testing.T) {
	ids := strings.Join(removedViews, "|")
	link := regexp.MustCompile(`(view=|views\.|nav:|view\s*=\s*"|View:\s*"|views\s*=\s*\[[^\]]*")(` + ids + `)\b`)
	roots := []string{"../../profiles", "../../plugins", "../web/ui/src", "../client/menubar", "../cli"}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || isTest(path) {
				return err
			}
			switch filepath.Ext(path) {
			case ".toml", ".md", ".go", ".ts", ".svelte":
			default:
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for i, line := range strings.Split(string(b), "\n") {
				if m := link.FindString(line); m != "" {
					t.Errorf("%s:%d names a removed view: %s", path, i+1, m)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func isTest(path string) bool {
	base := filepath.Base(path)
	return strings.HasSuffix(base, "_test.go") || strings.Contains(base, ".test.") || strings.Contains(path, string(filepath.Separator)+"testdata"+string(filepath.Separator))
}
