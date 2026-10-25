package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/brand/brandtest"
)

func parse(t *testing.T, src string) (*token.FileSet, *ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	return fset, f
}

const importsClaude = `package cli
import "github.com/wloophq/wloop/internal/adapter/agent/claude"
var _ = claude.Kind{}
`

func TestR1FlagsACliFileImportingClaude(t *testing.T) {
	_, f := parse(t, importsClaude)
	for _, file := range []string{"internal/cli/round.go", "internal/cli/cli.go", "internal/cli/serve.go"} {
		if got := ruleR1(file, f); len(got) != 1 || !strings.Contains(got[0], "only the composition root picks providers") {
			t.Fatalf("%s: %q", file, got)
		}
	}
	for _, file := range []string{"internal/cli/root.go", "cmd/" + brandtest.DefaultName + "/main.go", "internal/e2e/live.go", "internal/adapter/agent/claude/home.go"} {
		if got := ruleR1(file, f); len(got) != 0 {
			t.Errorf("%s: %q", file, got)
		}
	}
	_, orca := parse(t, `package orca
import "github.com/wloophq/wloop/internal/workspace/orca"
`)
	if got := ruleR1("internal/adapter/orca/orca.go", orca); len(got) != 0 {
		t.Errorf("one provider in two packages: %q", got)
	}
	if got := ruleR1("internal/adapter/agent/echo/echo.go", f); len(got) != 1 {
		t.Errorf("echo importing claude: %q", got)
	}
	_, source := parse(t, `package cli
import "github.com/wloophq/wloop/internal/adapter/source/github"
`)
	for file, want := range map[string]int{"internal/cli/sources.go": 0, "internal/cli/serve.go": 1, "internal/cli/root.go": 0, "internal/cli/watch.go": 1, "internal/daemon/daemon.go": 1, "internal/adapter/source/slack/slack.go": 1} {
		if got := ruleR1(file, source); len(got) != want {
			t.Errorf("%s importing the GitHub source: %q", file, got)
		}
	}
	_, facts := parse(t, `package ghsource
import "github.com/wloophq/wloop/internal/adapter/facts/github"
`)
	for file, want := range map[string]int{"internal/adapter/source/github/module.go": 0, "internal/adapter/source/slack/module.go": 1, "internal/cli/serve.go": 1, "internal/cli/root.go": 0} {
		if got := ruleR1(file, facts); len(got) != want {
			t.Errorf("%s importing the GitHub facts: %q", file, got)
		}
	}
	_, store := parse(t, `package cli
import "github.com/wloophq/wloop/internal/adapter/store/sqlite"
`)
	if got := ruleR1("internal/cli/doctor.go", store); len(got) != 0 {
		t.Errorf("the store is no provider: %q", got)
	}
}

func TestR2FlagsCLAUDEInALiteral(t *testing.T) {
	fset, f := parse(t, `package cli
const a = "CLAUDE_CONFIG_DIR"
const b = "run Claude Code"
const c = "~/.claude"
const d = "mcp__loop__turn_end"
const e = "claudette is fine"
const g = "ORCA_TERMINAL_HANDLE"
`)
	got := ruleR2(fset, "internal/cli/x.go", f, nil, map[int]bool{})
	if len(got) != 5 {
		t.Fatalf("flagged %q", got)
	}
	if got := ruleR2(fset, "internal/adapter/agent/claude/x.go", f, nil, map[int]bool{}); len(got) != 0 {
		t.Fatalf("an adapter flagged: %q", got)
	}
}

func TestR2IgnoresImportPaths(t *testing.T) {
	fset, f := parse(t, importsClaude)
	if got := ruleR2(fset, "internal/cli/cli.go", f, nil, map[int]bool{}); len(got) != 0 {
		t.Fatalf("flagged an import path: %q", got)
	}
}

func TestR2StaleAllowlistFails(t *testing.T) {
	fset, f := parse(t, `package cli
var kinds = map[string]int{"claude": 1}
`)
	allow := []allowed{{"internal/cli/cli.go", "claude", "registry key"}, {"internal/cli/cli.go", "codex", "gone"}}
	used := map[int]bool{}
	if got := ruleR2(fset, "internal/cli/cli.go", f, allow, used); len(got) != 0 {
		t.Fatalf("an allowed literal flagged: %q", got)
	}
	if got := stale(allow, used); len(got) != 1 || !strings.Contains(got[0], `"codex"`) {
		t.Fatalf("stale %q", got)
	}
	if got := ruleR2(fset, "internal/cli/other.go", f, allow, map[int]bool{}); len(got) != 1 {
		t.Fatalf("allowed in another file: %q", got)
	}
}

func TestR3FlagsParseOutsideAdapters(t *testing.T) {
	_, f := parse(t, `package cli
import dturn "github.com/wloophq/wloop/internal/domain/turn"
var s = dturn.ParseAgentState("done")
`)
	if got := ruleR3("internal/cli/watch.go", f); len(got) != 1 {
		t.Fatalf("cli: %q", got)
	}
	for _, file := range []string{"internal/workspace/orca/orca.go", "internal/adapter/store/inventory/store.go"} {
		if got := ruleR3(file, f); len(got) != 0 {
			t.Errorf("%s: %q", file, got)
		}
	}
}

func TestSpawnRuleFlagsAPlantedPackage(t *testing.T) {
	_, f := parse(t, `package planted
import "os/exec"
func run() { _ = exec.CommandContext(nil, "x") }
`)
	_, aliased := parse(t, `package planted
import x "os/exec"
func run() { _ = x.Command("x") }
`)
	_, quiet := parse(t, `package planted
import "os/exec"
var _ exec.Cmd
`)
	for _, src := range []string{
		"package p\nimport \"os\"\nvar _, _ = os.StartProcess(\"x\", nil, nil)\n",
		"package p\nimport \"syscall\"\nvar _ = syscall.Exec(\"x\", nil, nil)\n",
		"package p\nimport \"syscall\"\nvar _, _ = syscall.ForkExec(\"x\", nil, nil)\n",
		"package p\nimport . \"os/exec\"\nvar _ Cmd\n",
	} {
		_, g := parse(t, src)
		if got := ruleSpawn("internal/planted", nil, []*ast.File{g}); len(got) != 1 {
			t.Errorf("%q: %q", src, got)
		}
	}
	meter := []string{module + "internal/meter"}
	if got := ruleSpawn("internal/planted", nil, []*ast.File{f}); len(got) != 1 || !strings.Contains(got[0], "internal/planted") {
		t.Fatalf("unmetered spawner: %q", got)
	}
	if got := ruleSpawn("internal/planted", nil, []*ast.File{aliased}); len(got) != 1 {
		t.Fatalf("aliased import: %q", got)
	}
	for name, got := range map[string][]string{
		"metered":  ruleSpawn("internal/planted", meter, []*ast.File{f}),
		"no spawn": ruleSpawn("internal/planted", nil, []*ast.File{quiet}),
		"exempt":   ruleSpawn("internal/cli", nil, []*ast.File{f}),
		"e2e":      ruleSpawn("internal/e2e", nil, []*ast.File{f}),
	} {
		if len(got) != 0 {
			t.Errorf("%s flagged: %q", name, got)
		}
	}
}
