package archtest

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// pluginIdentityAllowlist are the declarations that still tell a pull
// request from another unit by asking the GitHub plugin's own package, with
// how many times each asks.
var pluginIdentityAllowlist = []providerSite{
	{"internal/adapter/facts/session/facts.go", "repoOf", "port.PROf", 1, "debt: a session's facts read its linked pull request"},
	{"internal/adapter/store/inventory/byunit.go", "unitRef", "port.PROf", 1, "debt: the inventory keeps GitHub's pull request tables"},
	{"internal/brief/github_source.go", "GitHubSource.ref", "pr.RefOf", 1, "debt: the brief queue builds GitHub's brief source and its units outside the plugin"},
	{"internal/brief/queue.go", "Queue.source", "pr.Codec{}.Provider", 1, "debt: the brief queue builds GitHub's brief source and its units outside the plugin"},
	{"internal/brief/queue.go", "Queue.intent", "pr.Codec{}.Provider", 1, "debt: the brief queue builds GitHub's brief intent outside the plugin"},
	{"internal/brief/units.go", "refOf", "pr.RefOf", 1, "debt: the brief queue builds GitHub's brief source and its units outside the plugin"},
	{"internal/catalog/catalog.go", "withSource", "pr.Codec{}.Provider", 1, "debt: the catalog's built-in actions name the GitHub plugin"},
	{"internal/cli/launcher.go", "githubRef", "pr.Codec{}.Provider", 1, "debt: the launcher reads a pull request's workspace outside the plugin"},
	{"internal/cli/launcher.go", "cliLauncher.Launch", "pr.RefOf", 1, "debt: the launcher reads a pull request's workspace outside the plugin"},
	{"internal/cli/launcher.go", "isPR", "pr.RefOf", 1, "debt: the launcher reads a pull request's workspace outside the plugin"},
	{"internal/cli/serve_ended.go", "openPRBooks.Books", "pr.RefOf", 1, "debt: the ended turns' books are pull requests"},
	{"internal/core/port/port.go", "PROf", "pr.RefOf", 1, "debt: the core's pull request event helper, tracked in pluginDebt"},
	X,
	{"internal/daemon/actions.go", "Server.readAheadQueued", "isPullRequest", 1, "debt: the daemon gates the GitHub-only facilities on a pull request, see prAllowed"},
	{"internal/daemon/actions.go", "Server.runAction", "isPullRequest", 1, "debt: the daemon gates the GitHub-only facilities on a pull request, see prAllowed"},
	{"internal/daemon/api.go", "Server.handleRemoveDone", "isPullRequest", 1, "debt: the daemon gates the GitHub-only facilities on a pull request, see prAllowed"},
	{"internal/daemon/api.go", "Server.parsePR", "isPullRequest", 1, "debt: the daemon gates the GitHub-only facilities on a pull request, see prAllowed"},
	{"internal/daemon/attention_entries.go", "entryViewer.rowNotice", "isPullRequest", 1, "debt: the daemon gates the GitHub-only facilities on a pull request, see prAllowed"},
	{"internal/daemon/briefs.go", "refsOf", "isPullRequest", 1, "debt: the daemon gates the GitHub-only facilities on a pull request, see prAllowed"},
	{"internal/daemon/headless.go", "Server.runHeadless", "isPullRequest", 1, "debt: the daemon gates the GitHub-only facilities on a pull request, see prAllowed"},
	{"internal/daemon/plan.go", "Server.actOnPlanItem", "isPullRequest", 1, "debt: the daemon gates the GitHub-only facilities on a pull request, see prAllowed"},
	{"internal/daemon/refs.go", "isPullRequest", "port.PROf", 1, "debt: the daemon gates the GitHub-only facilities on a pull request, see prAllowed"},
	{"internal/daemon/refs.go", "refOf", "port.PROf", 1, "debt: the daemon gates the GitHub-only facilities on a pull request, see prAllowed"},
	{"internal/daemon/rows.go", "Server.detail", "isPullRequest", 1, "debt: the daemon gates the GitHub-only facilities on a pull request, see prAllowed"},
	{"internal/daemon/unitactions.go", "sourceFollowed", "isPullRequest", 1, "debt: the daemon gates the GitHub-only facilities on a pull request, see prAllowed"},
	{"internal/daemon/unitactions.go", "unitAction", "isPullRequest", 1, "debt: the daemon gates the GitHub-only facilities on a pull request, see prAllowed"},
}

// pluginIdentity flags, outside the providers' homes and the GitHub
// adapters, a question about a unit's provider asked of the GitHub plugin's
// package: pr.RefOf, pr.Codec{}.Provider(), port.PROf and the daemon's
// isPullRequest. A declaration may ask as many times as its entry says; used
// counts what each entry matched.
func pluginIdentity(fset *token.FileSet, file string, f *ast.File, allow []providerSite, used map[int]int) []string {
	dir := filepath.Dir(file)
	if testFile(file) || strings.HasSuffix(filepath.Base(dir), "test") || providerHome(file) || ownProvider(dir) == "github" {
		return nil
	}
	prName, portName := "pr", "port"
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		switch {
		case p == module+"internal/domain/pr" && imp.Name != nil:
			prName = imp.Name.Name
		case p == module+"internal/core/port" && imp.Name != nil:
			portName = imp.Name.Name
		}
	}
	pkgSel := func(e ast.Expr, pkg, name string) bool {
		s, ok := e.(*ast.SelectorExpr)
		if !ok || s.Sel.Name != name {
			return false
		}
		x, ok := s.X.(*ast.Ident)
		return ok && x.Name == pkg
	}
	asked := func(c *ast.CallExpr) string {
		switch fn := c.Fun.(type) {
		case *ast.Ident:
			if fn.Name == "isPullRequest" {
				return "isPullRequest"
			}
		case *ast.SelectorExpr:
			switch {
			case pkgSel(fn, prName, "RefOf"):
				return "pr.RefOf"
			case pkgSel(fn, portName, "PROf"):
				return "port.PROf"
			case fn.Sel.Name == "Provider":
				x := fn.X
				if p, ok := x.(*ast.ParenExpr); ok {
					x = p.X
				}
				if lit, ok := x.(*ast.CompositeLit); ok && pkgSel(lit.Type, prName, "Codec") {
					return "pr.Codec{}.Provider"
				}
			}
		}
		return ""
	}
	var out []string
	seen := map[int][]token.Pos{}
	names, nodes := decls(f)
	for j, d := range nodes {
		decl := names[j]
		ast.Inspect(d, func(n ast.Node) bool {
			c, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			what := asked(c)
			if what == "" {
				return true
			}
			for i, a := range allow {
				if a.file == file && a.decl == decl && a.literal == what {
					used[i]++
					seen[i] = append(seen[i], c.Pos())
					return true
				}
			}
			out = append(out, fmt.Sprintf("%s: %s in %s asks the GitHub plugin's package what a unit is; ask the unit's codec or a capability", fset.Position(c.Pos()), what, decl))
			return true
		})
	}
	for i, ps := range seen {
		if a := allow[i]; len(ps) > a.n {
			out = append(out, fmt.Sprintf("%s: %s in %s asked %d times, its allowlist entry allows %d (%s); a new question is a new site",
				fset.Position(ps[a.n]), a.literal, a.decl, len(ps), a.n, a.reason))
		}
	}
	return out
}

// TestProviderIdentityThroughAPluginPackageOnlyShrinks checks that code
// outside a provider's homes tells a pull request from another unit through
// the GitHub plugin's package only where pluginIdentityAllowlist lists it.
func TestProviderIdentityThroughAPluginPackageOnlyShrinks(t *testing.T) {
	t.Parallel()
	used := map[int]int{}
	forEachFile(t, false, func(fset *token.FileSet, file string, f *ast.File) {
		for _, b := range pluginIdentity(fset, file, f, pluginIdentityAllowlist, used) {
			t.Error(b)
		}
	})
	for _, s := range staleSites(pluginIdentityAllowlist, used) {
		t.Error(s)
	}
}

func TestPluginIdentityFlagsAPlantedQuestion(t *testing.T) {
	t.Parallel()
	fset, f := parse(t, `package x
import dpr "github.com/wloophq/wloop/internal/domain/pr"
func open(u unit.Unit) {
	if _, ok := dpr.RefOf(u); ok {
	}
	_ = (dpr.Codec{}).Provider()
	_ = port.PROf(ev)
	_ = isPullRequest(u)
	_ = other.RefOf(u)
}
`)
	none := map[int]int{}
	flagged(t, "outside", pluginIdentity(fset, "internal/daemon/x.go", f, nil, none), 4, "asks the GitHub plugin's package")
	flagged(t, "the plugin's own package", pluginIdentity(fset, "internal/domain/pr/x.go", f, nil, none), 0, "")
	flagged(t, "a GitHub adapter", pluginIdentity(fset, "internal/adapter/source/github/x.go", f, nil, none), 0, "")
	flagged(t, "a test file", pluginIdentity(fset, "internal/daemon/x_test.go", f, nil, none), 0, "")
	allow := []providerSite{{"internal/daemon/x.go", "open", "pr.RefOf", 1, "planted"}, {"internal/daemon/x.go", "open", "isPullRequest", 2, "planted"}}
	used := map[int]int{}
	flagged(t, "allowlisted", pluginIdentity(fset, "internal/daemon/x.go", f, allow, used), 2, "asks the GitHub plugin's package")
	flagged(t, "an entry allowing more", staleSites(allow, used), 1, "allows 2, matches 1")
	fset, f = parse(t, `package x
func open(u unit.Unit) {
	_, _ = pr.RefOf(u)
	_, _ = pr.RefOf(v)
}
`)
	used = map[int]int{}
	flagged(t, "a new question in a listed declaration", pluginIdentity(fset, "internal/daemon/x.go", f, allow[:1], used), 1, "a new question is a new site")
}
