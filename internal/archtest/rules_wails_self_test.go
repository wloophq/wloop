package archtest

import (
	"go/parser"
	"go/token"
	"testing"
)

func TestTheWailsRuleFlagsAPlantedImport(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	src := "package cli\n\nimport (\n\t\"fmt\"\n\t\"github.com/wailsapp/wails/v3/pkg/application\"\n)\n"
	f, err := parser.ParseFile(fset, "internal/cli/nativehost.go", src, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	flagged(t, "the composition root", ruleWailsImport(fset, "internal/cli/nativehost.go", f), 1, "internal/cli/nativehost.go:5")
	flagged(t, "the adapter", ruleWailsImport(fset, "internal/client/window/wails/host.go", f), 0, "")
}
