package archtest

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// rulePluginSDK flags an import under internal/ by a package of an external
// plugin, which builds against the SDK alone and runs as its own process.
func rulePluginSDK(self string, imports []string) []string {
	if !externalPlugin(self) {
		return nil
	}
	var out []string
	for _, imp := range imports {
		if dep, ok := local(imp); ok && under(dep, "internal") {
			out = append(out, fmt.Sprintf("%s imports %s; an external plugin imports only the SDK, under pkg/, and no package under internal/ (R-plugin)", self, imp))
		}
	}
	return out
}

var listedPlugins = sync.OnceValues(func() ([]pkg, error) {
	cmd := exec.Command("go", "list", "-json", "./plugins/...")
	cmd.Dir = filepath.Join("..", "..")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var all []pkg
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for dec.More() {
		var p pkg
		if err := dec.Decode(&p); err != nil {
			return nil, err
		}
		all = append(all, p)
	}
	return all, nil
})

// TestSlackPluginImportsOnlyTheSDK checks that no package under
// `plugins/slack`, tests included, imports a package under `internal/`:
// the Slack plugin builds against the SDK alone.
func TestSlackPluginImportsOnlyTheSDK(t *testing.T) {
	t.Parallel()
	checkPluginImportsOnlyTheSDK(t, "plugins/slack")
}

// TestGitHubPluginImportsOnlyTheSDK checks that no package under
// `plugins/github`, tests included, imports a package under `internal/`:
// the GitHub plugin builds against the SDK alone.
func TestGitHubPluginImportsOnlyTheSDK(t *testing.T) {
	t.Parallel()
	checkPluginImportsOnlyTheSDK(t, "plugins/github")
}

func checkPluginImportsOnlyTheSDK(t *testing.T, dir string) {
	t.Helper()
	ps, err := listedPlugins()
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, p := range ps {
		self, ok := local(p.ImportPath)
		if !ok || !under(self, dir) {
			continue
		}
		seen = true
		for _, b := range rulePluginSDK(self, slices.Concat(p.Imports, p.TestImports, p.XTestImports)) {
			t.Error(b)
		}
	}
	if !seen {
		t.Errorf("%s is missing from the package list", dir)
	}
}
