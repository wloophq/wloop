package archtest

import (
	"fmt"
	"path"
	"slices"
	"strings"
	"testing"
)

var pluginRoots = map[string][]string{
	"github": {
		"internal/adapter/source/github",
		"internal/adapter/facts/github",
		"internal/readmodel/prrow",
		"internal/situation",
		"internal/domain/github",
		"internal/domain/pr",
		"internal/domain/inventory",
		"internal/domain/change",
		"internal/github",
	},
	"slack": {
		"internal/adapter/source/slack",
		"internal/domain/slack",
		"internal/slack",
	},
}

var coreRoots = []string{
	"internal/core",
	"internal/domain",
	"internal/readmodel",
	"internal/api",
	"internal/web",
	"internal/client",
}

// pluginOf is the plugin a package belongs to, "" for none.
func pluginOf(pkgPath string) string {
	for id, roots := range pluginRoots {
		if under(pkgPath, roots...) {
			return id
		}
	}
	return ""
}

func corePackage(pkgPath string) bool {
	return under(pkgPath, coreRoots...) && pluginOf(pkgPath) == ""
}

func pluginTestFake(pkgPath string) bool {
	return pluginOf(pkgPath) != "" && strings.HasSuffix(path.Base(pkgPath), "test")
}

// debt is an import edge R16 forbids and lets pass, with why it exists.
type debt struct {
	from, to, reason string
}

var pluginDebt = []debt{
	{"internal/api", "internal/domain/pr", "debt: the reconcile report, the turn book, events, explore candidates and remove-done results carry a pr.Ref where the API should carry a unit.Unit"},
	{"internal/api/client", "internal/domain/pr", "debt: TurnBook and Facts take a pr.Ref, following the API"},
	{"internal/client/tui", "internal/domain/pr", "debt: the filter jumps to a pasted pull request URL with pr.ParseURL, and shortRef shortens an owner/repo#n label with pr.ParseKey; a client parses what the daemon should resolve"},
	{"internal/client/turncli", "internal/domain/pr", "debt: unitArg parses a pull request URL argument with pr.ParseURL; a client parses what the daemon should resolve"},
	{"internal/core/port", "internal/domain/pr", "debt: PROf reads a pull request from a github event"},
	{"internal/core/sweep", "internal/domain/inventory", "debt: the Inputs port returns the stored pull request rows as inventory.PR"},
	{"internal/core/sweep", "internal/domain/pr", "debt: the sweep's Facts embed pr.Facts"},
	{"internal/core/usage", "internal/domain/pr", "debt: a Turn names its pull request as a pr.Ref"},
	{"internal/readmodel", "internal/domain/inventory", "debt: the RowDeps.Intake hook takes an inventory.BriefRecord; tests seed rows, events and overrides"},
	{"internal/readmodel", "internal/domain/pr", "fix: test only: tests build pr.Ref units and parse row keys with pr.ParseKey"},
	{"internal/readmodel", "internal/readmodel/prrow", "fix: test only: tests build the GitHub row provider and read its payloads and renudge delay"},
	{"internal/readmodel", "internal/situation", "fix: test only: tests read the loop-waiting and muted row kinds and situation.WorkingDuration"},
	{"internal/web", "internal/situation", "fix: test only: the board and dashboard tests list situation kinds and use WorkingDuration"},
}

// ruleR16 flags an import of a plugin package by a core package, or by a
// package of another plugin, counting test imports; a test import of a
// plugin's test fake and the edges in debts pass, and used marks the debts
// that matched.
func ruleR16(self string, imports, testImports []string, debts []debt, used map[int]bool) []string {
	own := pluginOf(self)
	if own == "" && !corePackage(self) {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	check := func(imp string, test bool) {
		dep, internal := local(imp)
		if !internal || seen[dep] {
			return
		}
		seen[dep] = true
		target := pluginOf(dep)
		if target == "" || target == own || (test && pluginTestFake(dep)) {
			return
		}
		if i := slices.IndexFunc(debts, func(d debt) bool { return d.from == self && d.to == dep }); i >= 0 {
			used[i] = true
			return
		}
		if own == "" {
			out = append(out, fmt.Sprintf("%s imports %s of the %s plugin; the core never imports a plugin (R16)", self, dep, target))
		} else {
			out = append(out, fmt.Sprintf("%s of the %s plugin imports %s of the %s plugin; no plugin imports another (R16)", self, own, dep, target))
		}
	}
	for _, imp := range imports {
		check(imp, false)
	}
	for _, imp := range testImports {
		check(imp, true)
	}
	return out
}

func staleDebt(debts []debt, used map[int]bool) []string {
	var out []string
	for i, d := range debts {
		if !used[i] {
			out = append(out, fmt.Sprintf("pluginDebt entry %s -> %s (%s) matches no import; remove it (R16)", d.from, d.to, d.reason))
		}
	}
	return out
}

// TestCoreImportsNoPlugin checks that the core imports no plugin, no plugin
// imports another, and `pluginDebt` only shrinks.
func TestCoreImportsNoPlugin(t *testing.T) {
	t.Parallel()
	used := map[int]bool{}
	cores, plugins := 0, 0
	for _, p := range packages(t) {
		self, ok := local(p.ImportPath)
		if !ok {
			continue
		}
		switch {
		case corePackage(self):
			cores++
		case pluginOf(self) != "":
			plugins++
		}
		for _, b := range ruleR16(self, p.Imports, slices.Concat(p.TestImports, p.XTestImports), pluginDebt, used) {
			t.Error(b)
		}
	}
	if cores == 0 || plugins == 0 {
		t.Errorf("scanned %d core and %d plugin packages; the roots match nothing", cores, plugins)
	}
	for _, s := range staleDebt(pluginDebt, used) {
		t.Error(s)
	}
}

func TestR16FlagsAPlantedImport(t *testing.T) {
	t.Parallel()
	slack, gh := module+"internal/domain/slack", module+"internal/adapter/source/github"
	r16 := func(self string, imports, testImports []string) []string {
		return ruleR16(self, imports, testImports, nil, map[int]bool{})
	}
	flagged(t, "core imports slack", r16("internal/core/turns", []string{"context", module + "internal/domain/unit", slack}, nil), 1, "the core never imports a plugin (R16)")
	flagged(t, "slack imports github", r16("internal/domain/slack", []string{gh}, nil), 1, "no plugin imports another (R16)")
	flagged(t, "core test imports slack", r16("internal/core/links", nil, []string{slack}), 1, "(R16)")
	flagged(t, "core x-test imports a slack rows package", r16("internal/web", nil, []string{module + "internal/adapter/source/slack/rows"}), 1, "(R16)")
	flagged(t, "a plugin imports itself", r16("internal/adapter/source/github", []string{module + "internal/domain/pr", module + "internal/github"}, nil), 0, "")
	flagged(t, "a plugin imports the core", r16("internal/adapter/source/slack", []string{module + "internal/core/items", module + "internal/domain/unit"}, nil), 0, "")
	flagged(t, "outside core and plugins", r16("internal/daemon", []string{slack, gh}, nil), 0, "")
	flagged(t, "a core sibling of a plugin root", r16("internal/domain/prx", []string{slack}, nil), 1, "(R16)")
}

func TestR16ExemptsPluginTestFakes(t *testing.T) {
	t.Parallel()
	fake := module + "internal/adapter/source/github/ghsourcetest"
	r16 := func(imports, testImports []string) []string {
		return ruleR16("internal/core/views", imports, testImports, nil, map[int]bool{})
	}
	flagged(t, "a test import of a fake", r16(nil, []string{fake, module + "internal/slack/slacktest"}), 0, "")
	flagged(t, "a test import of the plugin", r16(nil, []string{module + "internal/adapter/source/github"}), 1, "(R16)")
	flagged(t, "a non-test import of a fake", r16([]string{fake}, []string{fake}), 1, "(R16)")
	flagged(t, "a core fake is not a plugin", r16(nil, []string{module + "internal/core/views/viewstest"}), 0, "")
}

func TestR16FlagsStaleDebt(t *testing.T) {
	t.Parallel()
	slack := module + "internal/domain/slack"
	debts := []debt{
		{"internal/core/turns", "internal/domain/slack", "planted"},
		{"internal/core/links", "internal/domain/slack", "gone"},
	}
	used := map[int]bool{}
	flagged(t, "a debt edge", ruleR16("internal/core/turns", []string{slack}, nil, debts, used), 0, "")
	flagged(t, "a debt edge is per importer", ruleR16("internal/core/items", []string{slack}, nil, debts, used), 1, "(R16)")
	flagged(t, "stale", staleDebt(debts, used), 1, "internal/core/links -> internal/domain/slack (gone) matches no import")
}
