package archtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// provider is the provider an import path or a file's package belongs to,
// "" for none: an agent under internal/adapter/agent, a view sink under
// internal/adapter/view, a source under internal/adapter/source with its
// workflow facts under internal/adapter/facts, a workspace manager whose
// internal/workspace/<x> and internal/adapter/<x> are one provider, and each
// other adapter but the store.
func provider(pkgPath string) string {
	parts := strings.Split(pkgPath, "/")
	switch {
	case under(pkgPath, "internal/adapter/store", "internal/workspace/fake"):
		return ""
	case under(pkgPath, "internal/adapter/facts"):
		if len(parts) < 4 {
			return ""
		}
		return "adapter/source/" + parts[3]
	case under(pkgPath, "internal/adapter/agent", "internal/adapter/view", "internal/adapter/source"):
		if len(parts) < 4 {
			return ""
		}
		return strings.Join(parts[1:4], "/")
	case under(pkgPath, "internal/adapter", "internal/workspace"):
		if len(parts) < 3 {
			return ""
		}
		return parts[2]
	}
	return ""
}

func compositionRoot(file string) bool {
	return file == "internal/cli/root.go" || under(path.Dir(file), "cmd", "internal/e2e")
}

// sourceRoot is the one file that names the sources; slackWiring files name
// only the Slack source.
const (
	sourceRoot  = "internal/cli/sources.go"
	slackWiring = "internal/cli/slack*.go"
)

func mayNameSource(file, dep string) bool {
	if file == sourceRoot {
		return under(dep, sourceAdapters)
	}
	wiring, _ := path.Match(slackWiring, file)
	return wiring && under(dep, sourceAdapters+"/slack")
}

// ruleR1 flags an import of a provider package outside that provider and the
// composition root; a source may also be imported by the files that name it.
func ruleR1(file string, f *ast.File) []string {
	var out []string
	own := provider(path.Dir(file))
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		dep, ok := local(p)
		if !ok {
			continue
		}
		prov := provider(dep)
		picks := compositionRoot(file) || mayNameSource(file, dep)
		if prov != "" && prov != own && !picks {
			out = append(out, fmt.Sprintf("%s imports %s; only the composition root picks providers (C4)", file, dep))
		}
	}
	return out
}

var providerToken = regexp.MustCompile(`(?i)(^|[^a-z])(claude|codex|gemini|haiku|sonnet|opus|orca)([^a-z]|$)|CLAUDE_|ORCA_|\.claude\b|mcp__`)

// allowed is a provider token a file outside the adapters may hold.
type allowed struct {
	file, literal, reason string
}

var allowlist = []allowed{
	{"internal/cli/root.go", "claude", "the registry key of the Claude agent entry"},
	{"internal/cli/root.go", "orca", "the registry key and the sink name of the Orca manager entry"},
	{"internal/cli/root.go", "Orca", "the Orca manager entry's display name"},
	{"internal/cli/root.go", "Claude Desktop", "the Claude Desktop manager entry's display name"},
	{"internal/config/config.go", "claude", "Defaults().Agent"},
	{"internal/config/config.go", "claude-desktop", "Defaults().WorkspaceManagers"},
	{"internal/config/config.go", "orca", "Defaults().WorkspaceManagers"},
	{"internal/intent/builtin/facts.go", "The workspace manager that owns the session, such as orca or claude-desktop.", "session.manager's doc names the managers' keys"},
	{"internal/intent/builtin/facts.go", "The dormant session's manager keeps its own record of it, and the user closed it there, such as archiving it in Claude Desktop.", "session.closed_by_manager's doc names the manager that keeps such a record"},
}

// fakes are the shared fakes of core ports, which may play a provider.
var fakes = []string{"internal/core/turns/turnstest", "internal/core/views/viewstest"}

// adapterFile is a file of an adapter or of a workspace manager's package.
func adapterFile(file string) bool {
	dir := path.Dir(file)
	return under(dir, "internal/adapter") || (under(dir, "internal/workspace") && provider(dir) != "")
}

func providerExempt(file string) bool {
	return adapterFile(file) || under(path.Dir(file), "internal/e2e") || file == "internal/turn/scenarios.go" || slices.Contains(fakes, path.Dir(file))
}

// ruleR2 flags a string literal naming a provider outside the adapters, but
// the allowlist's; used marks the allowlist entries that matched.
func ruleR2(fset *token.FileSet, file string, f *ast.File, allow []allowed, used map[int]bool) []string {
	if providerExempt(file) {
		return nil
	}
	imports := map[*ast.BasicLit]bool{}
	for _, imp := range f.Imports {
		imports[imp.Path] = true
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING || imports[lit] {
			return true
		}
		s, err := strconv.Unquote(lit.Value)
		if err != nil || !providerToken.MatchString(s) {
			return true
		}
		for i, a := range allow {
			if a.file == file && a.literal == s {
				used[i] = true
				return true
			}
		}
		out = append(out, fmt.Sprintf("%s: %q names a provider; provider tokens live in adapters and the composition root (C4)", fset.Position(lit.Pos()), s))
		return true
	})
	return out
}

// stale names each allowlist entry that matched nothing, so the list only
// shrinks.
func stale(allow []allowed, used map[int]bool) []string {
	var out []string
	for i, a := range allow {
		if !used[i] {
			out = append(out, fmt.Sprintf("allowlist entry %s %q (%s) matches nothing; remove it", a.file, a.literal, a.reason))
		}
	}
	return out
}

// ruleR3 flags a parse of an agent state outside the adapters, which alone
// read a manager's state names: a call of ParseAgentState or UnmarshalText,
// or a dot import of domain/turn that hides the former.
func ruleR3(file string, f *ast.File) []string {
	if adapterFile(file) {
		return nil
	}
	names := map[string]bool{}
	var out []string
	for _, imp := range f.Imports {
		if p, _ := strconv.Unquote(imp.Path.Value); p == module+"internal/domain/turn" {
			name := "turn"
			if imp.Name != nil {
				name = imp.Name.Name
			}
			if name == "." {
				out = append(out, fmt.Sprintf("%s dot-imports domain/turn; name the package so its agent state parse shows (B1)", file))
			}
			names[name] = true
		}
	}
	if len(names) == 0 {
		return out
	}
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		x, isIdent := sel.X.(*ast.Ident)
		if (sel.Sel.Name == "ParseAgentState" && isIdent && names[x.Name]) || sel.Sel.Name == "UnmarshalText" {
			out = append(out, fmt.Sprintf("%s parses an agent state; only the adapters read a manager's states (B1)", file))
		}
		return true
	})
	return out
}

var spawnExempt = map[string]string{
	"internal/cli":                     "one-shot user commands: bundle, install, launchctl",
	"internal/e2e":                     "the end-to-end harness",
	"internal/client/tui":              "the user's editor, run in the foreground on a prompt's text",
	"internal/adapter/store/storetest": "test support that reads the inventory with sqlite3 for the e2e harness",
}

// spawns reports whether f calls exec.Command or exec.CommandContext of
// os/exec, os.StartProcess, syscall.Exec or syscall.ForkExec, or dot-imports
// os/exec.
func spawns(f *ast.File) bool {
	names := map[string][]string{}
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		if p != "os/exec" && p != "os" && p != "syscall" {
			continue
		}
		name := path.Base(p)
		if imp.Name != nil {
			name = imp.Name.Name
		}
		if name == "." {
			if p == "os/exec" {
				return true
			}
			continue
		}
		names[name] = append(names[name], p)
	}
	calls := map[string][]string{
		"os/exec": {"Command", "CommandContext"},
		"os":      {"StartProcess"},
		"syscall": {"Exec", "ForkExec"},
	}
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return !found
		}
		if x, ok := sel.X.(*ast.Ident); ok {
			for _, p := range names[x.Name] {
				if slices.Contains(calls[p], sel.Sel.Name) {
					found = true
				}
			}
		}
		return !found
	})
	return found
}

// ruleSpawn flags a package whose files spawn a process without importing
// internal/meter.
func ruleSpawn(self string, imports []string, files []*ast.File) []string {
	if _, ok := spawnExempt[self]; ok || slices.Contains(imports, module+"internal/meter") {
		return nil
	}
	if slices.ContainsFunc(files, spawns) {
		return []string{fmt.Sprintf("%s spawns a process but does not import internal/meter (C9)", self)}
	}
	return nil
}

func TestSpawningPackagesImportMeter(t *testing.T) {
	for _, p := range packages(t) {
		self, ok := local(p.ImportPath)
		if !ok {
			continue
		}
		fset := token.NewFileSet()
		var files []*ast.File
		for _, name := range p.GoFiles {
			f, err := parser.ParseFile(fset, filepath.Join(p.Dir, name), nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			files = append(files, f)
		}
		for _, b := range ruleSpawn(self, p.Imports, files) {
			t.Error(b)
		}
	}
}
