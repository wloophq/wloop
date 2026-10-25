package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
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
	t.Parallel()
	_, f := parse(t, importsClaude)
	for _, file := range []string{"internal/cli/round.go", "internal/cli/cli.go", "internal/cli/serve.go"} {
		flagged(t, file, ruleR1(file, f), 1, "only the composition root picks providers")
	}
	for _, file := range []string{"internal/cli/root.go", "cmd/" + brandtest.DefaultName + "/main.go", "internal/e2e/live.go", "internal/adapter/agent/claude/home.go"} {
		flagged(t, file, ruleR1(file, f), 0, "")
	}
	_, orca := parse(t, `package orca
import "github.com/wloophq/wloop/internal/workspace/orca"
`)
	flagged(t, "one provider in two packages", ruleR1("internal/adapter/orca/orca.go", orca), 0, "")
	flagged(t, "echo importing claude", ruleR1("internal/adapter/agent/echo/echo.go", f), 1, "")
	_, source := parse(t, `package cli
import "github.com/wloophq/wloop/internal/adapter/source/github"
`)
	for file, want := range map[string]int{"internal/cli/sources.go": 0, "internal/cli/serve.go": 1, "internal/cli/root.go": 0, "internal/cli/watch.go": 1, "internal/daemon/daemon.go": 1, "internal/adapter/source/slack/slack.go": 1} {
		flagged(t, file+" importing the GitHub source", ruleR1(file, source), want, "")
	}
	_, facts := parse(t, `package ghsource
import "github.com/wloophq/wloop/internal/adapter/facts/github"
`)
	for file, want := range map[string]int{"internal/adapter/source/github/module.go": 0, "internal/adapter/source/slack/module.go": 1, "internal/cli/serve.go": 1, "internal/cli/root.go": 0} {
		flagged(t, file+" importing the GitHub facts", ruleR1(file, facts), want, "")
	}
	_, store := parse(t, `package cli
import "github.com/wloophq/wloop/internal/adapter/store/sqlite"
`)
	flagged(t, "the store is no provider", ruleR1("internal/cli/doctor.go", store), 0, "")
}

func TestR2FlagsCLAUDEInALiteral(t *testing.T) {
	t.Parallel()
	fset, f := parse(t, `package cli
const a = "CLAUDE_CONFIG_DIR"
const b = "run Claude Code"
const c = "~/.claude"
const d = "mcp__loop__turn_end"
const e = "claudette is fine"
const g = "ORCA_TERMINAL_HANDLE"
`)
	flagged(t, "the cli", ruleR2(fset, "internal/cli/x.go", f, nil, map[int]bool{}), 5, "")
	flagged(t, "an adapter", ruleR2(fset, "internal/adapter/agent/claude/x.go", f, nil, map[int]bool{}), 0, "")
}

func TestR2IgnoresImportPaths(t *testing.T) {
	t.Parallel()
	fset, f := parse(t, importsClaude)
	flagged(t, "an import path", ruleR2(fset, "internal/cli/cli.go", f, nil, map[int]bool{}), 0, "")
}

func TestR2StaleAllowlistFails(t *testing.T) {
	t.Parallel()
	fset, f := parse(t, `package cli
var kinds = map[string]int{"claude": 1}
`)
	allow := []allowed{{"internal/cli/cli.go", "claude", "registry key"}, {"internal/cli/cli.go", "codex", "gone"}}
	used := map[int]bool{}
	flagged(t, "an allowed literal", ruleR2(fset, "internal/cli/cli.go", f, allow, used), 0, "")
	flagged(t, "stale", stale(allow, used), 1, `"codex"`)
	flagged(t, "allowed in another file", ruleR2(fset, "internal/cli/other.go", f, allow, map[int]bool{}), 1, "")
}

func TestR3FlagsParseOutsideAdapters(t *testing.T) {
	t.Parallel()
	_, f := parse(t, `package cli
import dturn "github.com/wloophq/wloop/internal/domain/turn"
var s = dturn.ParseAgentState("done")
`)
	flagged(t, "cli", ruleR3("internal/cli/watch.go", f), 1, "")
	for _, file := range []string{"internal/workspace/orca/orca.go", "internal/adapter/store/inventory/store.go"} {
		flagged(t, file, ruleR3(file, f), 0, "")
	}
}

func TestSpawnRuleFlagsAPlantedPackage(t *testing.T) {
	t.Parallel()
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
		flagged(t, src, ruleSpawn("internal/planted", nil, []*ast.File{g}), 1, "")
	}
	meter := []string{module + "internal/meter"}
	flagged(t, "unmetered spawner", ruleSpawn("internal/planted", nil, []*ast.File{f}), 1, "internal/planted")
	flagged(t, "aliased import", ruleSpawn("internal/planted", nil, []*ast.File{aliased}), 1, "")
	for name, got := range map[string][]string{
		"metered":  ruleSpawn("internal/planted", meter, []*ast.File{f}),
		"no spawn": ruleSpawn("internal/planted", nil, []*ast.File{quiet}),
		"exempt":   ruleSpawn("internal/cli", nil, []*ast.File{f}),
		"e2e":      ruleSpawn("internal/e2e", nil, []*ast.File{f}),
	} {
		flagged(t, name, got, 0, "")
	}
}
