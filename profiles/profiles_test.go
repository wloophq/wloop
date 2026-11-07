package profiles

import (
	"bufio"
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// A user copies these files into a profile, so their comments say what a section holds, never the plan step that
// brought it.
var workNote = regexp.MustCompile(`(?i)^#\s*(each )?piece\b`)

func TestTheBundledProfilesCommentsNameNoPlanPiece(t *testing.T) {
	err := fs.WalkDir(Default, "default", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".toml") {
			return err
		}
		f, err := Default.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		s := bufio.NewScanner(f)
		for n := 1; s.Scan(); n++ {
			if workNote.MatchString(strings.TrimSpace(s.Text())) {
				t.Errorf("%s:%d: %s", path, n, s.Text())
			}
		}
		return s.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
}
