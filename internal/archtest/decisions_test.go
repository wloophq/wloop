package archtest

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/archtest/decisionlog"
)

var testFunc = regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]+)\(`)

// decisionTests flags a decision that names no test, or names one that no
// test file declares.
func decisionTests(ds []decisionlog.Decision, tests map[string]bool) []string {
	var out []string
	for _, d := range ds {
		if len(d.Tests) == 0 {
			out = append(out, fmt.Sprintf("decisions/%s.md names no test on its Enforced by line; an entry records a constraint a test keeps", d.Slug))
		}
		for _, name := range d.Tests {
			if !tests[name] {
				out = append(out, fmt.Sprintf("decisions/%s.md names %s, which no test file declares", d.Slug, name))
			}
		}
	}
	return out
}

// repoTests returns the test functions every _test.go file of the repository
// declares, whatever its build tags.
func repoTests(t *testing.T, root string) map[string]bool {
	t.Helper()
	tests := map[string]bool{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "node_modules" || d.Name() == ".git") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, m := range testFunc.FindAllSubmatch(b, -1) {
			tests[string(m[1])] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tests
}

// TestEveryDecisionNamesATestThatExists checks that every entry of the
// decision log names on its Enforced by line at least one test, and only
// tests the repository declares.
func TestEveryDecisionNamesATestThatExists(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..")
	ds, err := decisionlog.Read(filepath.Join(root, "docs", "src", "content", "docs", "architecture", "decisions"))
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range decisionTests(ds, repoTests(t, root)) {
		t.Error(b)
	}
}
