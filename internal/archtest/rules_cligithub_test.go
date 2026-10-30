package archtest

import (
	"fmt"
	"go/ast"
	"go/token"
	"maps"
	"slices"
	"strconv"
	"testing"
)

// githubClients are the packages that reach GitHub: the GitHub plugin's
// API client and gh.
var githubClients = []string{"plugins/github/api", "plugins/github/gh"}

// cliGitHubDebt are the test files of internal/cli that still import a
// GitHub client, with why.
var cliGitHubDebt = map[string]string{
	"internal/cli/githubplugin_test.go":  "the test's forge reads the fake GitHub as the plugin does, standing in for its binary",
	"internal/cli/harness_test.go":       "the harness reads a pull request the inventory lacks from the fake GitHub as the plugin does",
	"internal/cli/plugin_binary_test.go": "the test serves a fake GitHub to the plugin's binary",
}

// ruleCLIGitHubClient flags, in a file under internal/cli, an import of a
// GitHub client; a test file named in debts passes, and used records it.
func ruleCLIGitHubClient(file string, imports []string, debts map[string]string, used map[string]bool) []string {
	if !under(file, "internal/cli") {
		return nil
	}
	var out []string
	for _, imp := range imports {
		dep, internal := local(imp)
		if !internal || !under(dep, githubClients...) {
			continue
		}
		if _, ok := debts[file]; ok && testFile(file) {
			used[file] = true
			continue
		}
		out = append(out, fmt.Sprintf("%s imports %s; the CLI speaks to GitHub only through its plugin", file, imp))
	}
	return out
}

// TestCLIImportsNoGitHubClient checks that no file under `internal/cli`
// imports the GitHub plugin's API client or gh: the CLI reaches GitHub
// through its plugin's calls. The test files in `cliGitHubDebt` pass, and
// the list only shrinks.
func TestCLIImportsNoGitHubClient(t *testing.T) {
	t.Parallel()
	used := map[string]bool{}
	scanned := 0
	forEachFile(t, true, func(_ *token.FileSet, file string, f *ast.File) {
		if under(file, "internal/cli") {
			scanned++
		}
		for _, b := range ruleCLIGitHubClient(file, fileImports(f), cliGitHubDebt, used) {
			t.Error(b)
		}
	})
	if scanned == 0 {
		t.Error("no file under internal/cli")
	}
	for _, file := range slices.Sorted(maps.Keys(cliGitHubDebt)) {
		if !used[file] {
			t.Errorf("cliGitHubDebt entry %s imports no GitHub client; remove it", file)
		}
	}
}

func fileImports(f *ast.File) []string {
	out := make([]string, 0, len(f.Imports))
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		out = append(out, p)
	}
	return out
}
