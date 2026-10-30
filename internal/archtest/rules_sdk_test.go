package archtest

import (
	"fmt"
	"slices"
	"testing"
)

// ruleSDK flags an import of a package under internal/ by a package under
// pkg/, counting test imports.
func ruleSDK(self string, imports []string) []string {
	if !under(self, "pkg") {
		return nil
	}
	var out []string
	for _, imp := range imports {
		if dep, ok := local(imp); ok && under(dep, "internal") {
			out = append(out, fmt.Sprintf("%s imports %s; the plugin SDK builds outside this module, so it imports nothing under internal/ (R-sdk)", self, imp))
		}
	}
	return out
}

// TestTheSDKImportsNoInternal checks that no package under `pkg/`, tests
// included, imports a package under `internal/`: plugins built outside this
// module compile against it.
func TestTheSDKImportsNoInternal(t *testing.T) {
	t.Parallel()
	seen := false
	for _, p := range packages(t) {
		self, ok := local(p.ImportPath)
		if !ok {
			continue
		}
		seen = seen || self == "pkg/plugin"
		for _, b := range ruleSDK(self, slices.Concat(p.Imports, p.TestImports, p.XTestImports)) {
			t.Error(b)
		}
	}
	if !seen {
		t.Error("pkg/plugin is missing from the package list")
	}
}
