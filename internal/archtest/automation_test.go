package archtest

import (
	"fmt"
	"go/ast"
	"go/token"
	"path"
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

// providerSite is a place outside a provider's homes that may still spell a
// provider id: the declaration it sits in (a function, a method as
// Type.Method, or a package-level name) and how many times it spells it, so a
// new spelling in an allowlisted file fails the rule.
type providerSite struct {
	file, decl, literal string
	n                   int
	reason              string
}

// coreProviderAllowlist are the places that may still name a provider.
var coreProviderAllowlist = []providerSite{
	{"internal/core/turns/headless.go", "entryInput", `"entry"`, 1, "the headless turn's entry input, which shares the entry provider's spelling"},
	{"internal/adapter/facts/session/facts.go", "Provider.build", "unit.GitHub", 1, "debt: a session's facts read its linked pull request's state"},
	{"internal/adapter/orca/orca.go", "Adapter.Apply", "unit.GitHub", 1, "Orca links a worktree to a GitHub pull request only"},
	{"internal/workspace/orca/orca.go", "Manager.LinkUnit", "unit.GitHub", 1, "Orca's linkedPR holds a GitHub pull request only"},
	{"internal/adapter/store/inventory/activity.go", "Store.eachRef", "unit.GitHub", 1, "debt: the inventory keeps GitHub's pull request tables"},
	{"internal/adapter/store/inventory/briefruns.go", "githubBriefs", `"github"`, 1, "debt: the inventory keeps GitHub's pull request tables"},
	{"internal/adapter/store/inventory/store.go", "Store.StoredFacts", "unit.GitHub", 1, "debt: the inventory keeps GitHub's pull request tables"},
	{"internal/adapter/store/inventory/store.go", "refOf", "unit.GitHub", 1, "debt: the inventory keeps GitHub's pull request tables"},
	{"internal/adapter/store/inventory/store.go", "Store.Reviewed", "unit.GitHub", 1, "debt: the inventory keeps GitHub's pull request tables"},
	{"internal/adapter/store/inventory/store.go", "Store.Ended", "unit.GitHub", 1, "debt: the inventory keeps GitHub's pull request tables"},
	{"internal/adapter/store/inventory/store.go", "Store.FirstRoundBlocks", "unit.GitHub", 1, "debt: the inventory keeps GitHub's pull request tables"},
	{"internal/adapter/store/inventory/store.go", "Store.Tracked", "unit.GitHub", 1, "debt: the inventory keeps GitHub's pull request tables"},
	{"internal/adapter/store/inventory/store.go", "Store.UnitlessEvents", "unit.Session", 1, "the store keeps the sessions the core keeps"},
	{"internal/adapter/store/inventory/timings.go", "Store.activeRefs", "unit.GitHub", 1, "debt: the inventory keeps GitHub's pull request tables"},
	{"internal/adapter/store/sqlite/marks.go", "marksTopic", "unit.GitHub", 1, "debt: a round mark notes GitHub's rounds topic"},
	{"internal/adapter/store/sqlite/registry.go", "sessionUnit", "unit.Session", 1, "the store keeps the sessions the core keeps"},
	{"internal/adapter/store/sqlite/retain.go", "retainWorkflowEventsSQL", "unit.Session", 1, "the store keeps the sessions the core keeps"},
	{"internal/adapter/store/sqlite/retain.go", "retainWorkflowEventsSQL", "unit.Workspace", 1, "the store keeps the workspaces the core keeps"},
	{"internal/adapter/store/sqlite/slack.go", "slackProvider", `"slack"`, 1, "the store keeps the Slack plugin's tables"},
	{"internal/adapter/store/sqlite/turns.go", "prOf", "unit.GitHub", 1, "debt: a pull request's turns are keyed by owner, repository and number"},
	{"internal/adapter/store/sqlite/turns.go", "compareUnits", "unit.GitHub", 2, "debt: pull requests sort by owner, repository and number"},
	{"internal/cli/actions.go", "envActions.Intent", "unit.GitHub", 1, "debt: the cockpit's intent action takes the pull request path on GitHub units"},
	{"internal/cli/actions.go", "Env.fetchPR", "unit.GitHub", 1, "debt: the GitHub viewer read outside the plugin"},
	{"internal/cli/facts.go", "inventoryFacts.MyReviews", "unit.GitHub", 1, "debt: the GitHub viewer read outside the plugin"},
	{"internal/cli/sweepinputs.go", "storeInputs.Viewer", "unit.GitHub", 1, "debt: the GitHub viewer read outside the plugin"},
	{"internal/cli/turn.go", "Env.storedViewer", "unit.GitHub", 1, "debt: the GitHub viewer read outside the plugin"},
	{"internal/cli/cli.go", "commands", `"slack"`, 1, "the slack command, the Slack plugin's own commands"},
	{"internal/cli/help.go", "devCommand", `"slack"`, 1, "the slack command, the Slack plugin's own commands"},
	{"internal/cli/init_config.go", "mergeInitConfig", `"slack"`, 1, "the slack section of the config init writes"},
	{"internal/cli/intent_cmd.go", "intentTarget", "unit.GitHub", 1, "debt: the usage line names a pull request's target"},
	{"internal/cli/placement.go", "Env.placements", "unit.GitHub", 1, "debt: GitHub's default placement, wired outside the plugin's PlacementDefaults"},
	{"internal/cli/placement.go", "githubPlacement.Default", "unit.GitHub", 2, "debt: GitHub's default placement reads the pull request and the viewer outside the plugin"},
	{"internal/cli/serve.go", "serveAllDeps", "unit.GitHub", 4, "debt: the daemon's wiring of the brief queue, the GitHub viewer and the forge"},
	{"internal/cli/serve.go", "authorFacts", "unit.GitHub", 1, "debt: the views' author facts are the GitHub module's"},
	{"internal/cli/serve.go", "trackedBooks.Books", "unit.GitHub", 1, "debt: a carded pull request without a turn still gets an empty book, so its card stays"},
	{"internal/cli/serve_payloads.go", "Env.payloadReaders", "unit.GitHub", 1, "debt: GitHub's payload reader, wired outside the plugin's Payload"},
	{"internal/cli/serve_plugins.go", "withoutGitHub", "unit.GitHub", 1, "debt: the GitHub facilities the daemon holds go with the GitHub plugin"},
	{"internal/cli/storedfacts.go", "storedFacts.Facts", "unit.GitHub", 1, "debt: the stored facts of a pull request's turn"},
	{"internal/cli/targets.go", "Env.providerIDs", "unit.GitHub", 1, "debt: GitHub's provider listed before the plugins"},
	{"skills/skills.go", "Bundled", "unit.GitHub", 1, "debt: a turn skill takes a pull request's URL"},
	{"internal/meter/meter.go", "GitHub", `"github"`, 1, "the meter's resource of a GitHub API request, not a unit provider"},
}

// providerHomes are the packages a provider id may appear in outside the
// core: each plugin's own packages, the vocabulary tables and the unit
// package that declares the ids. The composition root and the files that
// name the sources are allowed too, and a provider's adapters for that
// provider's own id.
var providerHomes = []string{
	"internal/domain/unit",
	"internal/domain/pr", "internal/readmodel/prrow", "internal/situation", "internal/github",
	"internal/slack", "internal/domain/slack",
	"internal/intent/builtin",
	"internal/e2e",
	// The codecs of the providers the core keeps.
	"internal/domain/gtd/codec.go", "internal/domain/turn/session.go", "internal/domain/workspace",
}

// providerHome says whether file may spell any provider id.
func providerHome(file string) bool {
	dir := filepath.Dir(file)
	if under(file, providerHomes...) || under(dir, providerHomes...) || compositionRoot(file) || file == sourceRoot {
		return true
	}
	wiring, _ := filepath.Match(slackWiring, file)
	return wiring
}

// ownProvider is the provider id an adapter's package may spell: the last
// element of its provider() name, "" outside the adapters.
func ownProvider(dir string) string {
	p := provider(dir)
	if p == "" {
		return ""
	}
	return path.Base(p)
}

// constIDs are the ids the unit provider constants hold.
var constIDs = map[string]unit.ProviderID{"GitHub": unit.GitHub, "Session": unit.Session, "Workspace": unit.Workspace, "Entry": unit.Entry}

// decls are f's top-level declarations by name: a function, a method as
// Type.Method, and each var, const and type by its first name.
func decls(f *ast.File) (names []string, nodes []ast.Node) {
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			names, nodes = append(names, funcName(d)), append(nodes, d)
		case *ast.GenDecl:
			for _, s := range d.Specs {
				switch s := s.(type) {
				case *ast.ValueSpec:
					names, nodes = append(names, s.Names[0].Name), append(nodes, s)
				case *ast.TypeSpec:
					names, nodes = append(names, s.Name.Name), append(nodes, s)
				}
			}
		}
	}
	return names, nodes
}

func funcName(d *ast.FuncDecl) string {
	if d.Recv == nil || len(d.Recv.List) == 0 {
		return d.Name.Name
	}
	t := d.Recv.List[0].Type
	if s, ok := t.(*ast.StarExpr); ok {
		t = s.X
	}
	if g, ok := t.(*ast.IndexExpr); ok {
		t = g.X
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name + "." + d.Name.Name
	}
	return d.Name.Name
}

// coreLiterals flags a provider id spelled in a non-test file: in the core,
// any provider id as a string literal or a unit provider constant; outside
// it and outside the providers' homes, a unit provider constant or a
// plugin's id as a literal or a person key's prefix, except an adapter's
// own provider's id. Fakes, packages named <x>test, are excluded. A
// declaration may spell an allowlisted literal as many times as its entry
// says; used counts the spellings each entry matched.
func coreLiterals(fset *token.FileSet, file string, f *ast.File, allow []providerSite, used map[int]int) []string {
	dir := filepath.Dir(file)
	if testFile(file) || strings.HasSuffix(filepath.Base(dir), "test") {
		return nil
	}
	core := under(dir, "internal/core")
	if !core && providerHome(file) {
		return nil
	}
	own := ownProvider(dir)
	imports := map[*ast.BasicLit]bool{}
	unitName := "unit"
	for _, imp := range f.Imports {
		imports[imp.Path] = true
		if p, _ := strconv.Unquote(imp.Path.Value); p == module+"internal/domain/unit" && imp.Name != nil {
			unitName = imp.Name.Name
		}
	}
	plugin := func(s string) bool { return slices.Contains(pluginIDs(), s) }
	where := "the core names no provider"
	if !core {
		where = "a provider id lives in its plugin, the vocabulary tables and the composition root"
	}
	var out []string
	seen := map[int][]token.Pos{}
	names, nodes := decls(f)
	for j, d := range nodes {
		decl := names[j]
		flag := func(pos token.Pos, spelled, id string) {
			if !core && own != "" && id == own {
				return
			}
			for i, a := range allow {
				if a.file == file && a.decl == decl && a.literal == spelled {
					used[i]++
					seen[i] = append(seen[i], pos)
					return
				}
			}
			out = append(out, fmt.Sprintf("%s: %s in %s; %s", fset.Position(pos), spelled, decl, where))
		}
		ast.Inspect(d, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.BasicLit:
				s, err := strconv.Unquote(n.Value)
				if err != nil || n.Kind != token.STRING || imports[n] {
					return true
				}
				prefix, _, keyed := strings.Cut(s, ":")
				switch {
				case core && providerIDs[s], !core && plugin(s):
					flag(n.Pos(), strconv.Quote(s), s)
				case !core && keyed && plugin(prefix):
					flag(n.Pos(), strconv.Quote(s), prefix)
				}
			case *ast.SelectorExpr:
				if x, ok := n.X.(*ast.Ident); ok && x.Name == unitName && providerConsts[n.Sel.Name] {
					flag(n.Pos(), "unit."+n.Sel.Name, string(constIDs[n.Sel.Name]))
				}
			}
			return true
		})
	}
	for i, ps := range seen {
		if a := allow[i]; len(ps) > a.n {
			out = append(out, fmt.Sprintf("%s: %s in %s spelled %d times, its allowlist entry allows %d (%s); a new spelling is a new site",
				fset.Position(ps[a.n]), a.literal, a.decl, len(ps), a.n, a.reason))
		}
	}
	return out
}

// staleSites names each allowlist entry that matched fewer spellings than it
// allows, so the list only shrinks.
func staleSites(allow []providerSite, used map[int]int) []string {
	var out []string
	for i, a := range allow {
		if used[i] < a.n {
			out = append(out, fmt.Sprintf("allowlist entry %s %s %s (%s) allows %d, matches %d; lower or remove it", a.file, a.decl, a.literal, a.reason, a.n, used[i]))
		}
	}
	return out
}

// TestTheCoreNamesNoProvider checks that the core spells no provider id,
// and that outside the core a provider id appears only in its plugin's
// packages, the vocabulary tables, the unit package and the composition
// root.
func TestTheCoreNamesNoProvider(t *testing.T) {
	t.Parallel()
	used := map[int]int{}
	forEachFile(t, false, func(fset *token.FileSet, file string, f *ast.File) {
		for _, b := range coreLiterals(fset, file, f, coreProviderAllowlist, used) {
			t.Error(b)
		}
	})
	for _, s := range staleSites(coreProviderAllowlist, used) {
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
	none := map[int]int{}
	flagged(t, "literals", coreLiterals(fset, "internal/core/automation/x.go", f, nil, none), 4, "the core names no provider")
	flagged(t, "another core package", coreLiterals(fset, "internal/core/gtd/x.go", f, nil, none), 4, "the core names no provider")
	flagged(t, "a test file", coreLiterals(fset, "internal/core/automation/x_test.go", f, nil, none), 0, "")
	flagged(t, "the fakes", coreLiterals(fset, "internal/core/automation/automationtest/x.go", f, nil, none), 0, "")
	outside := map[string]int{"internal/daemon/x.go": 3, "internal/client/tui/x.go": 3, "internal/cli/root.go": 0, "internal/cli/sources.go": 0,
		"internal/cli/slack_wire.go": 0, "internal/domain/pr/x.go": 0, "internal/intent/builtin/x.go": 0,
		"internal/adapter/source/slack/x.go": 2, "internal/adapter/source/github/x.go": 2, "internal/adapter/facts/session/x.go": 2, "internal/workspace/orca/x.go": 3}
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
	allow := []providerSite{{"internal/core/gtd/x.go", "b", "unit.Session", 1, "planted"}, {"internal/core/gtd/x.go", "a", `"slack"`, 1, "stale"}}
	used := map[int]int{}
	flagged(t, "allowlisted", coreLiterals(fset, "internal/core/gtd/x.go", f, allow, used), 3, "the core names no provider")
	flagged(t, "stale", staleSites(allow, used), 1, "matches 0")
	fset, f = parse(t, `package x
func (s *server) open() {
	if u.Provider == unit.GitHub {
	}
}
func other() { _ = unit.GitHub }
`)
	site := []providerSite{{"internal/cli/x.go", "server.open", "unit.GitHub", 1, "planted"}}
	used = map[int]int{}
	flagged(t, "another declaration of an allowlisted file", coreLiterals(fset, "internal/cli/x.go", f, site, used), 1, "unit.GitHub in other;")
	flagged(t, "the allowlisted site", staleSites(site, used), 0, "")
	fset, f = parse(t, `package x
func (s *server) open() {
	if u.Provider == unit.GitHub {
	}
	if v.Provider == unit.GitHub {
	}
}
`)
	used = map[int]int{}
	flagged(t, "a new spelling in an allowlisted declaration", coreLiterals(fset, "internal/cli/x.go", f, site, used), 1, "spelled 2 times, its allowlist entry allows 1")
	site[0].n = 3
	flagged(t, "an entry allowing more than its declaration spells", staleSites(site, used), 1, "allows 3, matches 2")
	fset, f = parse(t, `package x
import dunit "github.com/wloophq/wloop/internal/domain/unit"
var a = dunit.GitHub
`)
	flagged(t, "an aliased import", coreLiterals(fset, "internal/intent/x.go", f, nil, none), 1, "a provider id lives in its plugin")
}
