package archtest

import (
	"fmt"
	"testing"
)

// readModelBans are what the read model reaches only through its ports:
// brief and situation pull the store and GitHub in transitively.
var readModelBans = []string{"internal/adapter", "internal/github",
	"internal/brief", "internal/situation", "internal/workspace/orca"}

// ruleR15 flags an import of an adapter, GitHub,
// the brief queue, the situation deriver or the Orca client by the read
// model; its PR provider, internal/readmodel/prrow, is excluded by name.
func ruleR15(self string, imports []string) []string {
	if !under(self, "internal/readmodel") || under(self, "internal/readmodel/prrow") {
		return nil
	}
	var out []string
	for _, imp := range imports {
		if dep, internal := local(imp); internal && under(dep, readModelBans...) {
			out = append(out, fmt.Sprintf("%s imports %s; the read model reads its inputs through ports (R15)", self, imp))
		}
	}
	return out
}

// TestReadModelImports checks that the read model reaches its inputs only
// through ports.
func TestReadModelImports(t *testing.T) {
	t.Parallel()
	seen := false
	for _, p := range packages(t) {
		self, ok := local(p.ImportPath)
		if !ok {
			continue
		}
		seen = seen || self == "internal/readmodel"
		for _, b := range ruleR15(self, p.Imports) {
			t.Error(b)
		}
	}
	if !seen {
		t.Error("internal/readmodel is missing from the package list")
	}
}

func TestR15FlagsAPlantedImport(t *testing.T) {
	t.Parallel()
	ok := []string{"context", "time", module + "internal/api", module + "internal/catalog", module + "internal/core/port",
		module + "internal/domain/round", module + "internal/domain/unit"}
	flagged(t, "allowed", ruleR15("internal/readmodel", ok), 0, "")
	bad := append(ok, module+"internal/adapter/store/inventory", module+"internal/github", module+"internal/adapter/source/github",
		module+"internal/brief", module+"internal/situation", module+"internal/workspace/orca")
	flagged(t, "root", ruleR15("internal/readmodel", bad), 6, "(R15)")
	flagged(t, "another provider", ruleR15("internal/readmodel/threadrow", bad), 6, "(R15)")
	flagged(t, "the PR provider", ruleR15("internal/readmodel/prrow", bad), 0, "")
	flagged(t, "a sibling of prrow", ruleR15("internal/readmodel/prrowx", bad), 6, "(R15)")
	flagged(t, "not the read model", ruleR15("internal/daemon", bad), 0, "")
	flagged(t, "the workspace cache", ruleR15("internal/readmodel", []string{module + "internal/workspace"}), 0, "")
}
