package archtest

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
)

var loopRoots = []string{"internal/core/turns", "internal/core/intake", "internal/daemon"}

// prAllowed are the loop files that may import the pull request domain, each
// with the reason it names a pull request by nature.
var prAllowed = map[string]string{
	"internal/daemon/explore.go":        "explore lists GitHub search candidates, pull requests by nature",
	"internal/daemon/timings.go":        "review timings read pr.Facts",
	"internal/daemon/timings_derive.go": "review timings read pr.Facts",
	"internal/daemon/probe.go":          "the e2e probe names a pull request",
	"internal/daemon/refs.go":           "the daemon's one edge to pull request references, for the GitHub-only roles keyed by one",
	"internal/daemon/briefs.go":         "the brief queue is keyed by pull request",
}

const prDomain = module + "internal/domain/pr"

// loopPRImports flags an import of the pull request domain by a loop file
// that is not in allowed.
func loopPRImports(file string, imports []string, allowed map[string]string) []string {
	if !under(filepath.Dir(file), loopRoots...) {
		return nil
	}
	if _, ok := allowed[file]; ok || !slices.Contains(imports, prDomain) {
		return nil
	}
	return []string{file + " imports internal/domain/pr; loop code is keyed by unit.Unit"}
}

func TestLoopCodeImportsNoPR(t *testing.T) {
	used := map[string]bool{}
	for _, p := range packages(t) {
		self, ok := local(p.ImportPath)
		if !ok || !under(self, loopRoots...) {
			continue
		}
		for _, name := range p.GoFiles {
			rel := filepath.ToSlash(filepath.Join(self, name))
			f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(p.Dir, name), nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			var imports []string
			for _, imp := range f.Imports {
				path, _ := strconv.Unquote(imp.Path.Value)
				imports = append(imports, path)
			}
			for _, b := range loopPRImports(rel, imports, prAllowed) {
				t.Error(b)
			}
			if slices.Contains(imports, prDomain) {
				used[rel] = true
			}
		}
	}
	for file, why := range prAllowed {
		if !used[file] {
			t.Errorf("%s is allowed to import internal/domain/pr (%s) but does not", file, why)
		}
	}
}

func TestLoopCodeImportsNoPRFlagsAPlantedImport(t *testing.T) {
	imports := []string{"context", prDomain}
	flagged(t, "a daemon file", loopPRImports("internal/daemon/rows.go", imports, nil), 1, "keyed by unit")
	flagged(t, "a turns subpackage", loopPRImports("internal/core/turns/turnstest/store.go", imports, nil), 1, "keyed by unit")
	flagged(t, "an allowed file", loopPRImports("internal/daemon/explore.go", imports, map[string]string{"internal/daemon/explore.go": "x"}), 0, "")
	flagged(t, "no pr import", loopPRImports("internal/daemon/rows.go", []string{"context"}, nil), 0, "")
	flagged(t, "the github source", loopPRImports("internal/adapter/source/github/source.go", imports, nil), 0, "")
	flagged(t, "a sibling of the daemon", loopPRImports("internal/daemonx/x.go", imports, nil), 0, "")
}
