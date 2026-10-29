package intent

import (
	"io/fs"
	"os"
	"strings"
	"testing"
)

// promptRoots are every bundled text an agent reads: the default profile, the plugins' profiles and the
// skills.
var promptRoots = []string{"../../profiles/default", "../../plugins/slack/profile", "../../plugins/github", "../../skills"}

func TestNoPromptNamesAnInboxTool(t *testing.T) {
	read := 0
	for _, root := range promptRoots {
		err := fs.WalkDir(os.DirFS(root), ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !promptFile(path) {
				return err
			}
			raw, err := fs.ReadFile(os.DirFS(root), path)
			if err != nil {
				return err
			}
			read++
			for _, old := range []string{"inbox_post", "inbox_read", "Inbox message"} {
				if strings.Contains(string(raw), old) {
					t.Errorf("%s/%s names %q", root, path, old)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if read == 0 {
		t.Fatal("no prompt was read")
	}
}

func promptFile(path string) bool {
	for _, ext := range []string{".tmpl", ".md", ".toml"} {
		if strings.HasSuffix(path, ext) {
			return true
		}
	}
	return false
}
