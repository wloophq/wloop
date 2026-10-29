package archtest

import (
	"path/filepath"
	"strconv"
	"testing"
)

const (
	sourcesFile    = sourceRoot
	sourceAdapters = "internal/adapter/source"
)

// sourceImports flags a non-test file that names a source outside the one
// registration point: an import of a source adapter anywhere but
// internal/cli/sources.go, the files that wire one source and the sources
// themselves.
func sourceImports(file string, imports []string) []string {
	dir := filepath.Dir(file)
	if under(dir, sourceAdapters) {
		return nil
	}
	var out []string
	for _, imp := range imports {
		dep, internal := local(imp)
		switch {
		case !internal:
		case under(dep, sourceAdapters) && !mayNameSource(file, dep):
			out = append(out, file+" imports "+dep+"; only "+sourcesFile+" names a source")
		}
	}
	return out
}

// TestOnlySourcesGoNamesASource checks that only `cli/sources.go`, and
// for one source the files that wire it (`cli/slack*.go` and
// `cli/serve_slack.go` for Slack, `cli/serve_github.go` for GitHub), imports
// a source.
func TestOnlySourcesGoNamesASource(t *testing.T) {
	t.Parallel()
	seen := false
	for _, p := range packages(t) {
		self, ok := local(p.ImportPath)
		if !ok {
			continue
		}
		for _, name := range p.GoFiles {
			rel := filepath.ToSlash(filepath.Join(self, name))
			seen = seen || rel == sourcesFile
			_, f := parseFile(t, filepath.Join(p.Dir, name))
			var imports []string
			for _, imp := range f.Imports {
				path, _ := strconv.Unquote(imp.Path.Value)
				imports = append(imports, path)
			}
			for _, b := range sourceImports(rel, imports) {
				t.Error(b)
			}
		}
	}
	if !seen {
		t.Errorf("%s is missing from the package list", sourcesFile)
	}
}

func TestOnlySourcesGoNamesASourceFlagsAPlantedImport(t *testing.T) {
	t.Parallel()
	gh := module + "internal/adapter/source/github"
	imports := []string{"context", gh}
	flagged(t, "sources.go", sourceImports(sourcesFile, imports), 0, "")
	flagged(t, "another cli file", sourceImports("internal/cli/serve.go", imports), 1, "only "+sourcesFile)
	flagged(t, "the file that wires the source", sourceImports("internal/cli/serve_github.go", imports), 0, "")
	flagged(t, "the daemon", sourceImports("internal/daemon/daemon.go", imports), 1, "")
	flagged(t, "the read model", sourceImports("internal/readmodel/prrow/provider.go", []string{gh}), 1, "names a source")
	flagged(t, "a source", sourceImports("internal/adapter/source/github/cards.go", imports), 0, "")
	flagged(t, "a future source", sourceImports("internal/adapter/source/slack/module.go", imports), 0, "")
	flagged(t, "a sibling of sources", sourceImports("internal/adapter/sourcex/x.go", []string{gh}), 1, "names a source")
}

func TestSlackWiringFilesMayNameSlack(t *testing.T) {
	t.Parallel()
	eval := module + "internal/adapter/source/slack/eval"
	_, f := parse(t, `package cli
import "`+eval+`"
var _ = eval.Run
`)
	for _, c := range []struct {
		file string
		hits int
	}{
		{"internal/cli/slackeval.go", 0},
		{"internal/cli/slack.go", 0},
		{"internal/cli/turn.go", 1},
		{"internal/cli/other.go", 1},
		{"internal/cli/sub/slackeval.go", 1},
	} {
		flagged(t, c.file+" sourceImports", sourceImports(c.file, []string{eval}), c.hits, "only "+sourcesFile)
		flagged(t, c.file+" R1", ruleR1(c.file, f), c.hits, "(C4)")
	}
	gh := module + "internal/adapter/source/github"
	flagged(t, "a slack wiring file naming github", sourceImports("internal/cli/slackeval.go", []string{gh}), 1, "only "+sourcesFile)
}
