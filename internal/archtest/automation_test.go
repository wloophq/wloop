package archtest

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/domain/unit"
)

// engineBans are the provider-specific packages the engine never imports:
// providers are values its ports carry.
var engineBans = []string{"internal/domain/pr", "internal/domain/turn", "internal/domain/view",
	"internal/domain/workspace", "internal/domain/round", "internal/adapter"}

var providerIDs = map[string]bool{string(unit.GitHub): true, string(unit.Session): true, string(unit.Workspace): true,
	string(unit.Entry): true, "slack": true}

// providerConsts are the unit provider constants the core may not spell.
var providerConsts = map[string]bool{"GitHub": true, "Session": true, "Workspace": true, "Entry": true}

// engineImports flags an import of engineBans by internal/core/automation;
// its fakes, automationtest, are excluded.
func engineImports(self string, imports []string) []string {
	if self != "internal/core/automation" {
		return nil
	}
	var out []string
	for _, imp := range imports {
		if dep, internal := local(imp); internal && under(dep, engineBans...) {
			out = append(out, fmt.Sprintf("%s imports %s; the engine names no provider", self, imp))
		}
	}
	return out
}

// coreProviderAllowlist are the core files that may still name a provider.
var coreProviderAllowlist = []allowed{
	{"internal/core/turns/headless.go", `"entry"`, "the headless turn's entry input, which shares the entry provider's spelling"},
	{"internal/adapter/store/inventory/activity.go", "unit.GitHub", "debt: the inventory keeps GitHub's pull request tables"},
	{"internal/adapter/store/inventory/briefruns.go", `"github"`, "debt: the inventory keeps GitHub's pull request tables"},
	{"internal/adapter/store/inventory/store.go", "unit.GitHub", "debt: the inventory keeps GitHub's pull request tables"},
	{"internal/adapter/store/inventory/store.go", "unit.Session", "the store keeps the sessions the core keeps"},
	{"internal/adapter/store/inventory/timings.go", "unit.GitHub", "debt: the inventory keeps GitHub's pull request tables"},
	{"internal/adapter/store/sqlite/marks.go", "unit.GitHub", "debt: a round mark notes GitHub's rounds topic"},
	{"internal/adapter/store/sqlite/registry.go", "unit.Session", "the store keeps the sessions the core keeps"},
	{"internal/adapter/store/sqlite/retain.go", "unit.Session", "the store keeps the sessions the core keeps"},
	{"internal/adapter/store/sqlite/retain.go", "unit.Workspace", "the store keeps the workspaces the core keeps"},
	{"internal/adapter/store/sqlite/slack.go", `"slack"`, "the store keeps the Slack plugin's tables"},
	{"internal/adapter/store/sqlite/turns.go", "unit.GitHub", "debt: a pull request's turns are keyed by owner, repository and number"},
	{"internal/cli/actions.go", "unit.GitHub", "debt: the GitHub viewer and actions read outside the plugin"},
	{"internal/cli/facts.go", "unit.GitHub", "debt: the GitHub viewer read outside the plugin"},
	{"internal/cli/sweepinputs.go", "unit.GitHub", "debt: the GitHub viewer read outside the plugin"},
	{"internal/cli/turn.go", "unit.GitHub", "debt: the GitHub viewer read outside the plugin"},
	{"internal/cli/cli.go", `"slack"`, "the slack command, the Slack plugin's own commands"},
	{"internal/cli/help.go", `"slack"`, "the slack command, the Slack plugin's own commands"},
	{"internal/cli/init_config.go", `"slack"`, "the slack section of the config init writes"},
	{"internal/cli/intent_cmd.go", "unit.GitHub", "debt: the usage line names a pull request's target"},
	{"internal/cli/placement.go", "unit.GitHub", "debt: GitHub's default placement, wired outside the plugin's PlacementDefaults"},
	{"internal/cli/serve_payloads.go", "unit.GitHub", "debt: GitHub's payload reader, wired outside the plugin's Payload"},
	{"internal/cli/serve_plugins.go", "unit.GitHub", "debt: the GitHub facilities the daemon holds go with the GitHub plugin"},
	{"internal/cli/serve.go", "unit.GitHub", "debt: the daemon's wiring of the GitHub client, viewer and forge"},
	{"internal/cli/storedfacts.go", "unit.GitHub", "debt: the stored facts of a pull request's turn"},
	{"internal/cli/targets.go", "unit.GitHub", "debt: GitHub's provider listed before the plugins"},
	{"skills/skills.go", "unit.GitHub", "debt: a turn skill takes a pull request's URL"},
	{"internal/meter/meter.go", `"github"`, "the meter's resource of a GitHub API request, not a unit provider"},
}

// providerHomes are the packages a provider id may appear in outside the
// core: each plugin's own packages, the vocabulary tables and the unit
// package that declares the ids. A provider's adapters (provider() names
// one), the composition root and the files that name the sources are
// allowed too.
var providerHomes = []string{
	"internal/domain/unit",
	"internal/domain/pr", "internal/readmodel/prrow", "internal/situation", "internal/github",
	"internal/slack", "internal/domain/slack",
	"internal/intent/builtin",
	"internal/e2e",
	// The codecs of the providers the core keeps.
	"internal/domain/gtd/codec.go", "internal/domain/turn/session.go", "internal/domain/workspace",
}

// providerHome says whether file may spell a provider id.
func providerHome(file string) bool {
	dir := filepath.Dir(file)
	if under(file, providerHomes...) || under(dir, providerHomes...) || provider(dir) != "" || compositionRoot(file) || file == sourceRoot {
		return true
	}
	wiring, _ := filepath.Match(slackWiring, file)
	return wiring
}

// coreLiterals flags a provider id spelled in a non-test file: in the core,
// any provider id as a string literal or a unit provider constant; outside
// it and outside the providers' homes, a unit provider constant or a
// plugin's id as a literal or a person key's prefix. Fakes, packages named
// <x>test, are excluded. used marks the allowlist entries that matched.
func coreLiterals(fset *token.FileSet, file string, f *ast.File, allow []allowed, used map[int]bool) []string {
	dir := filepath.Dir(file)
	if testFile(file) || strings.HasSuffix(filepath.Base(dir), "test") {
		return nil
	}
	core := under(dir, "internal/core")
	if !core && providerHome(file) {
		return nil
	}
	imports := map[*ast.BasicLit]bool{}
	for _, imp := range f.Imports {
		imports[imp.Path] = true
	}
	plugin := func(s string) bool { return slices.Contains(pluginIDs(), s) }
	var out []string
	flag := func(pos token.Pos, spelled string) {
		for i, a := range allow {
			if a.file == file && a.literal == spelled {
				used[i] = true
				return
			}
		}
		where := "the core names no provider"
		if !core {
			where = "a provider id lives in its plugin, the vocabulary tables and the composition root"
		}
		out = append(out, fmt.Sprintf("%s: %s; %s", fset.Position(pos), spelled, where))
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.BasicLit:
			s, err := strconv.Unquote(n.Value)
			if err != nil || n.Kind != token.STRING || imports[n] {
				return true
			}
			prefix, _, keyed := strings.Cut(s, ":")
			if (core && providerIDs[s]) || (!core && (plugin(s) || keyed && plugin(prefix))) {
				flag(n.Pos(), strconv.Quote(s))
			}
		case *ast.SelectorExpr:
			if x, ok := n.X.(*ast.Ident); ok && x.Name == "unit" && providerConsts[n.Sel.Name] {
				flag(n.Pos(), "unit."+n.Sel.Name)
			}
		}
		return true
	})
	return out
}

// TestTheCoreNamesNoProvider checks that the core spells no provider id,
// and that outside the core a provider id appears only in its plugin's
// packages, the vocabulary tables, the unit package and the composition
// root.
func TestTheCoreNamesNoProvider(t *testing.T) {
	t.Parallel()
	used := map[int]bool{}
	forEachFile(t, false, func(fset *token.FileSet, file string, f *ast.File) {
		for _, b := range coreLiterals(fset, file, f, coreProviderAllowlist, used) {
			t.Error(b)
		}
	})
	for _, s := range stale(coreProviderAllowlist, used) {
		t.Error(s)
	}
}

// TestTheEngineImportsNoProvider checks that the automation engine imports no
// provider's domain.
func TestTheEngineImportsNoProvider(t *testing.T) {
	t.Parallel()
	seen := false
	for _, p := range packages(t) {
		self, ok := local(p.ImportPath)
		if !ok {
			continue
		}
		seen = seen || self == "internal/core/automation"
		for _, b := range engineImports(self, p.Imports) {
			t.Error(b)
		}
	}
	if !seen {
		t.Error("internal/core/automation is missing from the package list")
	}
}

func TestTheEngineRuleFlagsAPlantedProvider(t *testing.T) {
	t.Parallel()
	ok := []string{"context", module + "internal/core/port", module + "internal/domain/unit", module + "internal/domain/workflow"}
	flagged(t, "allowed", engineImports("internal/core/automation", ok), 0, "")
	bad := append(ok, module+"internal/domain/pr", module+"internal/domain/turn", module+"internal/domain/view",
		module+"internal/domain/workspace", module+"internal/domain/round", module+"internal/adapter/facts/github")
	flagged(t, "engine", engineImports("internal/core/automation", bad), 6, "names no provider")
	flagged(t, "its fakes", engineImports("internal/core/automation/automationtest", bad), 0, "")
	flagged(t, "another core package", engineImports("internal/core/turns", bad), 0, "")

	fset, f := parse(t, `package automation
var a = "github"
var b = unit.Session
var c = "sessions"
var d = workflow.Session
var e = unit.Unit{}
var f = unit.Entry
var g = "entry"
`)
	none := map[int]bool{}
	flagged(t, "literals", coreLiterals(fset, "internal/core/automation/x.go", f, nil, none), 4, "the core names no provider")
	flagged(t, "another core package", coreLiterals(fset, "internal/core/gtd/x.go", f, nil, none), 4, "the core names no provider")
	flagged(t, "a test file", coreLiterals(fset, "internal/core/automation/x_test.go", f, nil, none), 0, "")
	flagged(t, "the fakes", coreLiterals(fset, "internal/core/automation/automationtest/x.go", f, nil, none), 0, "")
	outside := map[string]int{"internal/daemon/x.go": 3, "internal/client/tui/x.go": 3, "internal/cli/root.go": 0, "internal/cli/sources.go": 0,
		"internal/cli/slack_wire.go": 0, "internal/domain/pr/x.go": 0, "internal/intent/builtin/x.go": 0, "internal/adapter/source/slack/x.go": 0}
	for file, n := range outside {
		fset, f := parse(t, `package x
var a = "github"
var b = unit.Session
var c = "session"
var d = "workspace"
var e = "slack:U1"
var g = "https://github.com/a/b"
`)
		flagged(t, file, coreLiterals(fset, file, f, nil, none), n, "a provider id lives in its plugin")
	}
	allow := []allowed{{"internal/core/gtd/x.go", "unit.Session", "planted"}, {"internal/core/gtd/x.go", `"slack"`, "stale"}}
	used := map[int]bool{}
	flagged(t, "allowlisted", coreLiterals(fset, "internal/core/gtd/x.go", f, allow, used), 3, "the core names no provider")
	flagged(t, "stale", stale(allow, used), 1, "(stale)")
}
