package archtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"
)

func flagged(t *testing.T, what string, got []string, n int, want string) {
	t.Helper()
	if len(got) != n {
		t.Errorf("%s: want %d hits, got %q", what, n, got)
		return
	}
	for _, g := range got {
		if !strings.Contains(g, want) {
			t.Errorf("%s: %q lacks %q", what, g, want)
		}
	}
}

func TestCatalogImportsFlagsTheDaemon(t *testing.T) {
	t.Parallel()
	imports := []string{
		"fmt", "slices",
		module + "internal/api",
		module + "internal/domain/intent",
		module + "internal/intent",
		module + "internal/intent/builtin",
		module + "internal/intent/expr",
		module + "internal/catalog",
	}
	flagged(t, "allowed", catalogImports(module+"internal/catalog", imports), 0, "")
	bad := append(imports, module+"internal/daemon", module+"internal/cli", module+"internal/adapter/store/sqlite", module+"internal/profile", "github.com/pelletier/go-toml/v2")
	flagged(t, "catalog", catalogImports(module+"internal/catalog", bad), 5, "the catalog reads facts, never the daemon, the cli or adapters")
	flagged(t, "subpackage", catalogImports(module+"internal/catalog/catalogtest", []string{module + "internal/daemon"}), 1, "the catalog reads facts")
	flagged(t, "not the catalog", catalogImports(module+"internal/daemon", bad), 0, "")
}

func TestLeafImportsFlagsEachLeaf(t *testing.T) {
	t.Parallel()
	flagged(t, "expr allowed", leafImports(module+"internal/intent/expr", []string{"fmt", "cel.dev/cel-go/cel", "cel.dev/cel-go/common/ast", module + "internal/domain/workflow"}), 0, "")
	flagged(t, "expr above the domain", leafImports(module+"internal/intent/expr", []string{module + "internal/intent", module + "internal/profile", "cel.dev/cel-gox", "github.com/google/cel-go/cel"}), 4, "internal/intent/expr")

	ok := []string{"slices", module + "internal/domain/intent", module + "internal/domain/record", module + "internal/intent/expr"}
	flagged(t, "builtin allowed", leafImports(module+"internal/intent/builtin", ok), 0, "")
	flagged(t, "builtin profile", leafImports(module+"internal/intent/builtin", append(ok, module+"internal/profile", module+"internal/intent")), 2, "internal/intent/builtin")

	toml := []string{"bytes", "github.com/pelletier/go-toml/v2", "github.com/pelletier/go-toml/v2/unstable"}
	flagged(t, "frontmatter allowed", leafImports(module+"internal/frontmatter", toml), 0, "")
	flagged(t, "frontmatter domain", leafImports(module+"internal/frontmatter", []string{module + "internal/domain/intent", "gopkg.in/yaml.v3"}), 2, "internal/frontmatter")

	flagged(t, "other package", leafImports(module+"internal/intent/builtin/cmd/factsdoc", []string{module + "internal/intent/builtin", "os"}), 0, "")
}

func TestTermfmtImportsFlagsLoopAndOtherModules(t *testing.T) {
	t.Parallel()
	ok := []string{"io", "regexp", "golang.org/x/term", "github.com/mattn/go-runewidth", "github.com/clipperhouse/uax29/v2/graphemes"}
	flagged(t, "allowed", termfmtImports(module+"internal/termfmt", ok), 0, "")
	bad := append(ok, module+"internal/api", "github.com/fatih/color", "golang.org/x/sys/windows", "github.com/mattn/go-runewidthx")
	flagged(t, "termfmt", termfmtImports(module+"internal/termfmt", bad), 4, "internal/termfmt imports")
	flagged(t, "not termfmt", termfmtImports(module+"internal/client/tui", bad), 0, "")
}

func TestLoaderImportsFlagsTurnAndTheDaemon(t *testing.T) {
	t.Parallel()
	ok := []string{
		"text/template",
		"github.com/pelletier/go-toml/v2", "github.com/pelletier/go-toml/v2/unstable",
		module + "internal/domain/intent", module + "internal/frontmatter",
		module + "internal/intent/builtin", module + "internal/intent/expr", module + "internal/profile",
	}
	flagged(t, "allowed", loaderImports(module+"internal/intent", ok), 0, "")
	bad := []string{module + "internal/turn", module + "internal/daemon", module + "internal/cli", module + "internal/catalog", module + "internal/core/turns", module + "internal/adapter/store/sqlite", module + "internal/config"}
	flagged(t, "loader", loaderImports(module+"internal/intent", bad), len(bad), "internal/intent imports")
	flagged(t, "a subpackage is a leaf's business", loaderImports(module+"internal/intent/expr", bad), 0, "")
}

// planted type-checks src as the package at path, resolving its imports from deps.
func planted(t *testing.T, fset *token.FileSet, path, file, src string, deps map[string]*types.Package) (*ast.File, *types.Info, *types.Package) {
	t.Helper()
	f, err := parser.ParseFile(fset, file, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Uses: map[*ast.Ident]types.Object{}, Defs: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{}}
	imp := importerFunc(func(p string) (*types.Package, error) {
		if pkg, ok := deps[p]; ok {
			return pkg, nil
		}
		return nil, fmt.Errorf("no planted package %s", p)
	})
	pkg, err := (&types.Config{Importer: imp}).Check(path, fset, []*ast.File{f}, info)
	if err != nil {
		t.Fatal(err)
	}
	return f, info, pkg
}

type importerFunc func(string) (*types.Package, error)

func (f importerFunc) Import(path string) (*types.Package, error) { return f(path) }

const plantedAPI = `package api
type ActionSpec struct { ID string; Input *InputSpec }
type InputSpec struct { Kind string }
type ActionState struct { ID string; Enabled bool }
type ActionsResponse struct { Specs []ActionSpec }
`

func TestActionLiteralsFlagsAStateBuiltOutsideTheCatalog(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	_, _, api := planted(t, fset, module+"internal/api", "api.go", plantedAPI, nil)
	deps := map[string]*types.Package{module + "internal/api": api}
	f, info, _ := planted(t, fset, module+"internal/daemon", "x.go", `package daemon
import "github.com/wloophq/wloop/internal/api"
var a = api.ActionState{ID: "skip", Enabled: true}
var b = []api.ActionSpec{{ID: "open"}}
var c = &api.InputSpec{Kind: "text"}
var d = api.ActionSpec{ID: "guide", Input: &api.InputSpec{Kind: "text"}}
var zero = api.ActionState{}
var states = map[string]api.ActionState{}
var resp = api.ActionsResponse{Specs: nil}
func mutate(s api.ActionState) api.ActionState { s.Enabled = false; return s }
`, deps)
	flagged(t, "daemon", actionLiterals(fset, "internal/daemon/x.go", info, f), 5, "only the catalog builds action specs and states")
	flagged(t, "catalog", actionLiterals(fset, "internal/catalog/actions.go", info, f), 0, "")
	flagged(t, "a test file", actionLiterals(fset, "internal/daemon/x_test.go", info, f), 0, "")
}

func TestProgramReadsFlagsAPredicateEvaluatedOutsideTheCatalog(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	_, _, loader := planted(t, fset, module+"internal/intent", "load.go", `package intent
type Program struct{}
func (*Program) Eval() bool { return true }
type Programs struct { Records, When, Primary *Program; Refuse []*Program; ActionTexts map[string]string }
type Loaded struct { Programs map[string]Programs }
type Other struct { When bool }
`, nil)
	deps := map[string]*types.Package{module + "internal/intent": loader}
	f, info, _ := planted(t, fset, module+"internal/daemon", "x.go", `package daemon
import "github.com/wloophq/wloop/internal/intent"
type wrap struct { intent.Programs }
func f(l *intent.Loaded, p *intent.Programs, w wrap, o intent.Other) bool {
	_ = p.ActionTexts["note"]
	_ = l.Programs["review"].Refuse[0].Eval()
	_ = w.Primary
	_ = p.Records
	_ = o.When
	_ = intent.Programs{When: nil}
	return p.When != nil
}
`, deps)
	flagged(t, "daemon", programReads(fset, "internal/daemon/x.go", info, f), 4, "only the loader and the catalog evaluate an intent's predicates")
	for _, file := range []string{"internal/catalog/intents.go", "internal/intent/validate.go", "internal/daemon/x_test.go"} {
		flagged(t, file, programReads(fset, file, info, f), 0, "")
	}
}

func TestBuiltinLiteralsFlagsANameOutsideTheTables(t *testing.T) {
	t.Parallel()
	fset, f := parse(t, `package daemon
import "github.com/wloophq/wloop/internal/intent/builtin"
const a = "pr.round_sent"
const b = "pr.addressed"
const c = "linked_else_main"
const d = "plan.cleared"
const e = "review"
const g = "pr.round_sent is fine inside a sentence"
var _ = builtin.Names
`)
	flagged(t, "daemon", builtinLiterals(fset, "internal/daemon/x.go", f), 4, "a builtin's name lives in its table")
	flagged(t, "a migration", builtinLiterals(fset, "internal/adapter/store/sqlite/migrate_v7_gates.go", f), 0, "")
	for _, file := range []string{"internal/intent/builtin/builtin.go", "internal/cli/builtins.go", "internal/daemon/builtins.go", "internal/daemon/x_test.go"} {
		flagged(t, file, builtinLiterals(fset, file, f), 0, "")
	}
}

func TestBuiltinLiteralsFlagsAStepNameOutsideTheTables(t *testing.T) {
	t.Parallel()
	fset, f := parse(t, `package cli
const a = "session.close"
const b = "workspace.remove"
const c = "session.mark_dormant"
const d = "classify"
`)
	flagged(t, "cli", builtinLiterals(fset, "internal/cli/automation.go", f), 3, "a builtin's name lives in its table")
	for _, file := range []string{"internal/cli/steps.go", "internal/cli/root.go", "internal/intent/builtin/builtin.go"} {
		flagged(t, file, builtinLiterals(fset, file, f), 0, "")
	}
}

func TestBuiltinLiteralsFlagsAnEffectNameOutsideTheTables(t *testing.T) {
	t.Parallel()
	fset, f := parse(t, `package cli
const a = "task.nudged"
const b = "unit.closed"
const c = "draft.propose"
`)
	flagged(t, "cli", builtinLiterals(fset, "internal/cli/serve_outbox.go", f), 3, "a builtin's name lives in its table")
	flagged(t, "the effects table", builtinLiterals(fset, "internal/cli/builtins_effects.go", f), 0, "")
}

func TestBuiltinNamesAreTheDottedAndSnakeNames(t *testing.T) {
	t.Parallel()
	names := builtinNames()
	var missing, extra []string
	for _, want := range []string{"pr.round_sent", "plan.cleared", "linked_else_main", "pr.addressed"} {
		if !names[want] {
			missing = append(missing, want)
		}
	}
	for _, not := range []string{"review", "author", "given", "", "take_over", "my_review"} {
		if names[not] {
			extra = append(extra, not)
		}
	}
	flagged(t, "builtin names missing", missing, 0, "")
	flagged(t, "no effect, prepare or workspace names with . or _", extra, 0, "")
}
