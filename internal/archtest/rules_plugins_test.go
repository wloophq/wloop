package archtest

import (
	"encoding/json"
	"fmt"
	"maps"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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
		"internal/github",
	},
	"slack": {
		"plugins/slack",
	},
}

var coreRoots = []string{
	"internal/core",
	"internal/domain",
	"internal/readmodel",
	"internal/api",
	"internal/web",
	"internal/client",
	"internal/catalog",
	"internal/keys",
	"internal/daemon",
	"internal/turn",
	"internal/brief",
	"internal/transport",
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

// debt is an import edge R16 forbids and lets pass, with why it exists;
// a test debt lets only the importer's test files pass.
type debt struct {
	from, to, reason string
	test             bool
}

var pluginDebt = []debt{
	{"internal/daemon", "internal/adapter/source/github", daemonTestsDebt, true},
	{"internal/daemon", "internal/adapter/source/github/present", daemonTestsDebt, true},
	{"internal/daemon", "internal/domain/github", daemonTestsDebt, true},
	{"internal/daemon", "internal/domain/inventory", daemonTestsDebt, true},
	{"internal/daemon", "internal/domain/pr", daemonTestsDebt, true},
	{"internal/daemon", "internal/github", daemonTestsDebt, true},
	{"internal/daemon", "internal/readmodel/prrow", daemonTestsDebt, true},
	{"internal/brief", "internal/domain/github", briefQueueDebt, false},
	{"internal/brief", "internal/domain/inventory", briefQueueDebt, false},
	{"internal/brief", "internal/domain/pr", briefQueueDebt, false},
	{"internal/brief", "internal/github", briefQueueDebt, false},
}

const briefQueueDebt = "debt: the brief queue keys its runs by pull request; it reads their input through the GitHub plugin's Briefer"

const daemonTestsDebt = "debt: the daemon's tests serve GitHub in process (explore.Explorer, prrow) while GitHub's rows, facts and cards stay in the host (issue 62)"

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
		if i := slices.IndexFunc(debts, func(d debt) bool { return d.from == self && d.to == dep && (test || !d.test) }); i >= 0 {
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

// TestCoreImportsNoPluginDomain checks that the core imports no plugin, no
// plugin imports another, and `pluginDebt` only shrinks.
func TestCoreImportsNoPluginDomain(t *testing.T) {
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
	if roots := slices.Sorted(maps.Keys(pluginRoots)); !slices.Equal(roots, pluginIDs()) {
		t.Errorf("pluginRoots name %v, the bundled manifests %v", roots, pluginIDs())
	}
	if cores == 0 || plugins == 0 {
		t.Errorf("scanned %d core and %d plugin packages; the roots match nothing", cores, plugins)
	}
	for _, s := range staleDebt(pluginDebt, used) {
		t.Error(s)
	}
}

// TestDaemonImportsNoPluginDomain checks that the daemon's own code imports
// no plugin package, under no debt: it reaches source logic through the
// plugins' Action and View only.
func TestDaemonImportsNoPluginDomain(t *testing.T) {
	t.Parallel()
	const daemon = "internal/daemon"
	if !corePackage(daemon) {
		t.Fatalf("%s is not among the core roots", daemon)
	}
	for _, d := range pluginDebt {
		if under(d.from, daemon) && !d.test {
			t.Errorf("pluginDebt lets the daemon's code import %s (%s); only its tests may", d.to, d.reason)
		}
	}
	found := false
	for _, p := range packages(t) {
		self, ok := local(p.ImportPath)
		if !ok || !under(self, daemon) {
			continue
		}
		found = true
		for _, b := range ruleR16(self, p.Imports, nil, nil, map[int]bool{}) {
			t.Error(b)
		}
	}
	if !found {
		t.Errorf("no package under %s", daemon)
	}
}

func TestR16FlagsAPlantedImport(t *testing.T) {
	t.Parallel()
	slack, gh := module+"plugins/slack/domain", module+"internal/adapter/source/github"
	r16 := func(self string, imports, testImports []string) []string {
		return ruleR16(self, imports, testImports, nil, map[int]bool{})
	}
	flagged(t, "core imports slack", r16("internal/core/turns", []string{"context", module + "internal/domain/unit", slack}, nil), 1, "the core never imports a plugin (R16)")
	flagged(t, "slack imports github", r16("plugins/slack/domain", []string{gh}, nil), 1, "no plugin imports another (R16)")
	flagged(t, "core test imports slack", r16("internal/core/links", nil, []string{slack}), 1, "(R16)")
	flagged(t, "core x-test imports a slack rows package", r16("internal/web", nil, []string{module + "plugins/slack/rows"}), 1, "(R16)")
	flagged(t, "a plugin imports itself", r16("internal/adapter/source/github", []string{module + "internal/domain/pr", module + "internal/github"}, nil), 0, "")
	flagged(t, "a plugin imports the core", r16("plugins/slack", []string{module + "internal/core/items", module + "internal/domain/unit"}, nil), 0, "")
	flagged(t, "outside core and plugins", r16("internal/cli", []string{slack, gh}, nil), 0, "")
	flagged(t, "a core sibling of a plugin root", r16("internal/domain/prx", []string{slack}, nil), 1, "(R16)")
	flagged(t, "the catalog imports the pull request domain", r16("internal/catalog", []string{module + "internal/domain/pr"}, nil), 1, "(R16)")
	flagged(t, "the keys import the GitHub change domain", r16("internal/keys", []string{module + "internal/domain/github/change"}, nil), 1, "(R16)")
}

func TestR16ExemptsPluginTestFakes(t *testing.T) {
	t.Parallel()
	fake := module + "internal/adapter/source/github/ghsourcetest"
	r16 := func(imports, testImports []string) []string {
		return ruleR16("internal/core/views", imports, testImports, nil, map[int]bool{})
	}
	flagged(t, "a test import of a fake", r16(nil, []string{fake, module + "plugins/slack/slacktest"}), 0, "")
	flagged(t, "a test import of the plugin", r16(nil, []string{module + "internal/adapter/source/github"}), 1, "(R16)")
	flagged(t, "a non-test import of a fake", r16([]string{fake}, []string{fake}), 1, "(R16)")
	flagged(t, "a core fake is not a plugin", r16(nil, []string{module + "internal/core/views/viewstest"}), 0, "")
}

func TestR16FlagsStaleDebt(t *testing.T) {
	t.Parallel()
	slack := module + "plugins/slack/domain"
	debts := []debt{
		{"internal/core/turns", "plugins/slack/domain", "planted", false},
		{"internal/core/links", "plugins/slack/domain", "gone", false},
	}
	used := map[int]bool{}
	flagged(t, "a debt edge", ruleR16("internal/core/turns", []string{slack}, nil, debts, used), 0, "")
	flagged(t, "a debt edge is per importer", ruleR16("internal/core/items", []string{slack}, nil, debts, used), 1, "(R16)")
	flagged(t, "stale", staleDebt(debts, used), 1, "internal/core/links -> plugins/slack/domain (gone) matches no import")
	tests := []debt{{"internal/core/items", "plugins/slack/domain", "fixtures", true}}
	flagged(t, "a test debt lets a test import", ruleR16("internal/core/items", nil, []string{slack}, tests, map[int]bool{}), 0, "")
	flagged(t, "a test debt is no code debt", ruleR16("internal/core/items", []string{slack}, nil, tests, map[int]bool{}), 1, "(R16)")
}

// pluginsRoot is the directory of the bundled plugins, one plugins/<id> each;
// its own package only embeds their manifests and profile layers.
const pluginsRoot = "plugins"

// pluginHome is the plugin directory pkgPath belongs to: plugins/<id>, the
// stand-in's directory, or pluginsRoot for the root package; "" for a host
// package.
func pluginHome(pkgPath string) string {
	switch {
	case under(pkgPath, standinDir):
		return standinDir
	case pkgPath == pluginsRoot:
		return pluginsRoot
	case under(pkgPath, pluginsRoot):
		return pluginDir(pkgPath)
	}
	return ""
}

// pluginDir is plugins/<id> of a path under plugins/<id>.
func pluginDir(path string) string {
	parts := strings.SplitN(path, "/", 3)
	return parts[0] + "/" + parts[1]
}

// rulePluginSDK flags an import under internal/, or of another plugin's
// package, by a plugin's package, which builds against the SDK and its own
// packages alone and runs as its own process.
func rulePluginSDK(self string, imports []string) []string {
	home := pluginHome(self)
	if home == "" {
		return nil
	}
	var out []string
	for _, imp := range imports {
		dep, ok := local(imp)
		switch {
		case !ok:
		case under(dep, "internal"):
			out = append(out, fmt.Sprintf("%s imports %s; a plugin imports only the SDK, under pkg/, and no package under internal/ (R-plugin)", self, imp))
		case pluginHome(dep) != "" && pluginHome(dep) != home:
			out = append(out, fmt.Sprintf("%s imports %s; a plugin imports no other plugin's package (R-plugin)", self, imp))
		}
	}
	return out
}

// listedPlugins are the packages under plugins/ and the stand-in's, which
// the host's package list leaves out.
var listedPlugins = sync.OnceValues(func() ([]pkg, error) {
	cmd := exec.Command("go", "list", "-json", "./"+pluginsRoot+"/...", "./"+standinDir+"/...")
	cmd.Dir = filepath.Join("..", "..")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var all []pkg
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for dec.More() {
		var p pkg
		if err := dec.Decode(&p); err != nil {
			return nil, err
		}
		all = append(all, p)
	}
	return all, nil
})

// TestPluginsImportOnlyTheSDK checks that no package under `plugins/`, nor
// the stand-in plugin's, imports a package under `internal/` or a package of
// another plugin, tests included: every plugin builds against the SDK and
// its own packages alone.
func TestPluginsImportOnlyTheSDK(t *testing.T) {
	t.Parallel()
	ps, err := listedPlugins()
	if err != nil {
		t.Fatal(err)
	}
	homes := map[string]bool{}
	for _, p := range ps {
		self, ok := local(p.ImportPath)
		if !ok {
			continue
		}
		homes[pluginHome(self)] = true
		for _, b := range rulePluginSDK(self, slices.Concat(p.Imports, p.TestImports, p.XTestImports)) {
			t.Error(b)
		}
	}
	for _, id := range pluginIDs() {
		if !homes[pluginsRoot+"/"+id] {
			t.Errorf("the bundled plugin %s has no package under %s/%s", id, pluginsRoot, id)
		}
	}
	if !homes[standinDir] || !homes[pluginsRoot] {
		t.Errorf("the package list lacks %s or %s: %v", standinDir, pluginsRoot, homes)
	}
}

// testOnly are the host packages only tests reach: a package whose path
// ends in "test" and that no package's code imports, other than another
// test-only package's.
func testOnly(ps []pkg) map[string]bool {
	importers := map[string][]string{}
	only := map[string]bool{}
	for _, p := range ps {
		self, ok := local(p.ImportPath)
		if !ok {
			continue
		}
		if strings.HasSuffix(path.Base(self), "test") {
			only[self] = true
		}
		for _, imp := range p.Imports {
			if dep, ok := local(imp); ok {
				importers[dep] = append(importers[dep], self)
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for p := range only {
			if slices.ContainsFunc(importers[p], func(by string) bool { return !only[by] }) {
				delete(only, p)
				changed = true
			}
		}
	}
	return only
}

// ruleHostPlugin flags a host package's code that imports a plugin's
// package; the root plugins package, which holds no plugin's code, and a
// test-only package pass.
func ruleHostPlugin(self string, imports []string, testOnly map[string]bool) []string {
	if pluginHome(self) != "" || testOnly[self] {
		return nil
	}
	var out []string
	for _, imp := range imports {
		if dep, ok := local(imp); ok && pluginHome(dep) != "" && dep != pluginsRoot {
			out = append(out, fmt.Sprintf("%s imports %s; the host runs a plugin as its own process and never links its code; only a test-only package, whose path ends in \"test\" and which only tests reach, may (R-host)", self, imp))
		}
	}
	return out
}

// ruleEmbedOnly flags an import of the root plugins package other than the
// standard library: it embeds the bundled manifests and profile layers and
// holds no plugin's code.
func ruleEmbedOnly(imports []string) []string {
	var out []string
	for _, imp := range imports {
		if !std(imp) {
			out = append(out, fmt.Sprintf("%s imports %s; it only embeds the bundled plugins' manifests and profile layers (R-host)", pluginsRoot, imp))
		}
	}
	return out
}

// TestHostImportsNoPlugin checks that no host package's code imports a
// plugin's package, so the host binary links no plugin. A test-only package,
// whose path ends in "test" and which only tests and other test-only
// packages import, such as `ghreporttest`, may run a plugin's code for the
// host's tests; the root `plugins` package, which the host reads the bundled
// manifests and profile layers from, imports only the standard library.
// Test files are left to R16.
func TestHostImportsNoPlugin(t *testing.T) {
	t.Parallel()
	host := packages(t)
	only := testOnly(host)
	if !only["internal/readmodel/prrow/ghreporttest"] {
		t.Errorf("ghreporttest is not test-only: %v", only)
	}
	for _, p := range host {
		self, _ := local(p.ImportPath)
		for _, b := range ruleHostPlugin(self, p.Imports, only) {
			t.Error(b)
		}
	}
	ps, err := listedPlugins()
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(ps, func(p pkg) bool { return p.ImportPath == module+pluginsRoot })
	if i < 0 {
		t.Fatalf("no package %s", pluginsRoot)
	}
	for _, b := range ruleEmbedOnly(ps[i].Imports) {
		t.Error(b)
	}
}
