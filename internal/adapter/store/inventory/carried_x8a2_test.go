package inventory

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// hostCopyWrites are the statements that would keep a copy of a GitHub
// plugin's rows in the host: its pull requests, their full reads and
// sightings, and its own overrides.
var hostCopyWrites = regexp.MustCompile(`(?i)(INSERT\s+(OR\s+\w+\s+)?INTO|UPDATE)\s+(pull_requests|pr_details|pr_seen)\b|SET\s+(tracked|taken_over)\s*=`)

// TestTheHostKeepsNoCopyOfGitHubsRows: the GitHub plugin's pull requests
// live in its own store and reach the host as its reports; nothing the
// host runs writes them into its own tables, and the inventory has no
// method that would.
func TestTheHostKeepsNoCopyOfGitHubsRows(t *testing.T) {
	t.Parallel()
	typ := reflect.TypeFor[*Store]()
	for _, m := range []string{"SaveReported", "UpsertPR", "SaveSnapshots", "SetReasons", "SetTracked", "SetAdopted", "SetWorkspace", "ReplaceWorkspaces",
		"PR", "PRs", "ListPRs", "Details", "Seen"} {
		if _, ok := typ.MethodByName(m); ok {
			t.Errorf("the inventory still has %s", m)
		}
	}
	root := filepath.Join("..", "..", "..")
	scanned := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" || strings.HasSuffix(d.Name(), "test") && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		base := filepath.Base(path)
		if !strings.HasSuffix(base, ".go") || strings.HasSuffix(base, "_test.go") || strings.HasPrefix(base, "migrate") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		scanned++
		if loc := hostCopyWrites.FindIndex(raw); loc != nil {
			t.Errorf("%s writes %q: the host keeps no copy of the GitHub plugin's rows", path, raw[loc[0]:loc[1]])
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if scanned < 100 {
		t.Fatalf("scanned %d files under internal/", scanned)
	}
}
