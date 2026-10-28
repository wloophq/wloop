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
)

// providerString is a file outside the plugins that may still spell a
// plugin's id inside its string literals, and how many times.
type providerString struct {
	file, provider string
	n              int
	reason         string
}

// historicalProviderStrings are the files whose provider ids are data
// already written: the store's migrations.
var historicalProviderStrings = []string{"internal/adapter/store/sqlite/migrate_v*.go"}

// providerStringAllowlist are the files outside the plugins that still spell
// a plugin's id in a string, with how many spellings each keeps.
var providerStringAllowlist = []providerString{
	{"internal/adapter/store/inventory/store.go", "github", 5, "debt: the inventory keeps GitHub's pull request tables (issue 62)"},
	{"internal/adapter/store/inventory/timings.go", "github", 3, "debt: the inventory keeps GitHub's pull request tables (issue 62)"},
	{"internal/adapter/store/sqlite/retain.go", "github", 21, "debt: retention drops a pull request's rows once GitHub says it ended (issue 62)"},
	{"internal/adapter/store/sqlite/retain.go", "slack", 3, "debt: retention tells a Slack unit gone by the Slack tables; the plugin should say (issue 62)"},
	{"internal/adapter/store/sqlite/retain_briefs.go", "github", 3, "debt: retention drops a pull request's rows once GitHub says it ended (issue 62)"},
	{"internal/adapter/store/sqlite/rounds.go", "github", 7, "debt: the round state keeps GitHub's review rounds (issue 62)"},
	{"internal/cli/doctor.go", "github", 1, "debt: the central fact and config tables name the plugins' facts and config keys, which the plugins should declare (issue 62)"},
	{"internal/config/config.go", "github", 7, "debt: the central fact and config tables name the plugins' facts and config keys, which the plugins should declare (issue 62)"},
	{"internal/intent/builtin/builtin.go", "slack", 1, "debt: the central fact and config tables name the plugins' facts and config keys, which the plugins should declare (issue 62)"},
	{"internal/intent/builtin/cmd/factsdoc/main.go", "slack", 1, "debt: the central fact and config tables name the plugins' facts and config keys, which the plugins should declare (issue 62)"},
	{"internal/intent/builtin/cmd/grammardoc/groups.go", "github", 8, "debt: the central fact and config tables name the plugins' facts and config keys, which the plugins should declare (issue 62)"},
	{"internal/intent/builtin/cmd/grammardoc/groups.go", "slack", 2, "debt: the central fact and config tables name the plugins' facts and config keys, which the plugins should declare (issue 62)"},
	{"internal/intent/builtin/facts.go", "github", 1, "debt: the central fact and config tables name the plugins' facts and config keys, which the plugins should declare (issue 62)"},
	{"internal/intent/builtin/grammar_config.go", "github", 25, "debt: the central fact and config tables name the plugins' facts and config keys, which the plugins should declare (issue 62)"},
	{"internal/config/plugins.go", "github", 1, "debt: GitHub's settings are a field of the configuration until its plugin reads its table through the broker (X8a2)"},
	{"internal/config/plugins.go", "slack", 1, "debt: Slack's settings are a field of the configuration while init and doctor read them there, until they ask the plugin (X9)"},
	{"internal/intent/builtin/renamed.go", "github", 7, "the renames name the keys of the configuration a release moved"},
	{"internal/intent/builtin/renamed.go", "slack", 9, "the renames name the keys of the configuration a release moved"},
	{"internal/intent/builtin/grammar_config.go", "slack", 30, "debt: the central fact and config tables name the plugins' facts and config keys, which the plugins should declare (issue 62)"},
}

// providerStringSpellings counts the spellings of id in the string s: s is
// id, or starts with "id:" (a person key), "id." (a plugin's fact) or
// "plugins.id." (a plugin's config key); or s quotes it inside, as 'id' (SQL), "id" or "id. (a tag, a
// key). An exact id and an "id:" prefix count only where whole is true:
// TestTheCoreNamesNoProvider counts them elsewhere.
func providerStringSpellings(s, id string, whole bool) int {
	n := strings.Count(s, "'"+id+"'") + strings.Count(s, `"`+id+`"`) + strings.Count(s, `"`+id+".")
	switch {
	case whole && (s == id || strings.HasPrefix(s, id+":")):
		n++
	case strings.HasPrefix(s, id+".") && !strings.HasPrefix(s, id+".com"), strings.HasPrefix(s, "plugins."+id+"."):
		n++
	}
	return n
}

// providerStringsExempt says whether file may spell id in its strings: a
// test or a fake, id's own plugin or adapter, the unit package that
// declares the ids, the composition root, the files that name the sources
// and, for Slack, its wiring.
func providerStringsExempt(file, id string) bool {
	dir := filepath.Dir(file)
	if testFile(file) || strings.HasSuffix(filepath.Base(dir), "test") || pluginOf(dir) == id || ownProvider(dir) == id {
		return true
	}
	if under(dir, "internal/domain/unit") || compositionRoot(file) || file == sourceRoot {
		return true
	}
	return wiresSource(file, id)
}

func historicalProviderString(file string) bool {
	return slices.ContainsFunc(historicalProviderStrings, func(g string) bool {
		ok, _ := filepath.Match(g, file)
		return ok
	})
}

// providerStrings counts, per plugin id, the spellings of the id in f's
// string literals, imports aside, with the position of each.
func providerStrings(fset *token.FileSet, file string, f *ast.File) map[string][]token.Position {
	if historicalProviderString(file) {
		return nil
	}
	whole := providerHome(file) && !under(filepath.Dir(file), "internal/core")
	imports := map[*ast.BasicLit]bool{}
	for _, imp := range f.Imports {
		imports[imp.Path] = true
	}
	out := map[string][]token.Position{}
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING || imports[lit] {
			return true
		}
		s, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		for _, id := range pluginIDs() {
			if providerStringsExempt(file, id) {
				continue
			}
			for range providerStringSpellings(s, id, whole) {
				out[id] = append(out[id], fset.Position(lit.Pos()))
			}
		}
		return true
	})
	return out
}

// providerStringBreaches compares the spellings found per file and
// provider with allow: more than an entry allows, or a spelling in a file
// with no entry, is a breach; fewer is a stale entry, so the list only
// shrinks.
func providerStringBreaches(found map[string]map[string][]token.Position, allow []providerString) []string {
	var out []string
	seen := map[[2]string]bool{}
	for _, a := range allow {
		seen[[2]string{a.file, a.provider}] = true
		got := found[a.file][a.provider]
		switch {
		case len(got) > a.n:
			out = append(out, fmt.Sprintf("%s: %s spells %q %d times, its allowlist entry allows %d (%s); a provider id lives in its plugin",
				got[a.n], a.file, a.provider, len(got), a.n, a.reason))
		case len(got) < a.n:
			out = append(out, fmt.Sprintf("allowlist entry %s %q (%s) allows %d, matches %d; lower or remove it", a.file, a.provider, a.reason, a.n, len(got)))
		}
	}
	files := make([]string, 0, len(found))
	for file := range found {
		files = append(files, file)
	}
	slices.Sort(files)
	for _, file := range files {
		for _, id := range pluginIDs() {
			if ps := found[file][id]; len(ps) > 0 && !seen[[2]string{file, id}] {
				out = append(out, fmt.Sprintf("%s: %q in a string outside its plugin; a provider id lives in its plugin", ps[0], id))
			}
		}
	}
	return out
}

// TestProviderIdsInStringsOutsideThePluginsOnlyShrink checks that a
// plugin's id spelled inside a Go string outside its plugin (an SQL
// literal 'github', a config key "plugins.slack.enabled", a struct tag) appears
// only in the store's migrations, which hold data already written, and in
// the files of a counted allowlist that only shrinks.
func TestProviderIdsInStringsOutsideThePluginsOnlyShrink(t *testing.T) {
	t.Parallel()
	found := map[string]map[string][]token.Position{}
	forEachFile(t, false, func(fset *token.FileSet, file string, f *ast.File) {
		if got := providerStrings(fset, file, f); len(got) > 0 {
			found[file] = got
		}
	})
	for _, b := range providerStringBreaches(found, providerStringAllowlist) {
		t.Error(b)
	}
}

func TestProviderStringsFlagAPlantedSpelling(t *testing.T) {
	t.Parallel()
	fset, f := parse(t, "package x\n"+
		"import _ \"github.com/wloophq/wloop/internal/slack\"\n"+
		"const q = `SELECT * FROM turns WHERE provider = 'github' OR provider = 'slack'`\n"+
		"type c struct{ S int `json:\"slack\"` }\n"+
		"var k = \"slack.enabled\"\n"+
		"var w = \"github\"\n"+
		"var u = \"https://github.com/a/b\"\n"+
		"var h = \"github.com/a/b\"\n"+
		"var p = \"githubber 'githubs'\"\n")
	count := func(file string) map[string]int {
		out := map[string]int{}
		for id, ps := range providerStrings(fset, file, f) {
			out[id] = len(ps)
		}
		return out
	}
	for file, want := range map[string]map[string]int{
		"internal/adapter/store/sqlite/x.go":            {"github": 1, "slack": 3},
		"internal/intent/builtin/x.go":                  {"github": 2, "slack": 3},
		"internal/adapter/store/sqlite/migrate_v5.go":   {},
		"internal/adapter/store/sqlite/x_test.go":       {},
		"internal/adapter/store/sqlite/sqlitetest/x.go": {},
		"internal/github/x.go":                          {"slack": 3},
		"internal/cli/slackdraft.go":                    {"github": 2},
		"internal/cli/root.go":                          {},
		"internal/domain/unit/x.go":                     {},
	} {
		got := count(file)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s: %v, want %v", file, got, want)
		}
	}
	found := map[string]map[string][]token.Position{"internal/adapter/store/sqlite/x.go": providerStrings(fset, "internal/adapter/store/sqlite/x.go", f)}
	flagged(t, "no entry", providerStringBreaches(found, nil), 2, "a provider id lives in its plugin")
	allow := []providerString{{"internal/adapter/store/sqlite/x.go", "github", 1, "planted"}, {"internal/adapter/store/sqlite/x.go", "slack", 2, "planted"}}
	flagged(t, "a new spelling", providerStringBreaches(found, allow), 1, `spells "slack" 3 times, its allowlist entry allows 2`)
	allow[1].n = 4
	flagged(t, "a stale entry", providerStringBreaches(found, allow), 1, "allows 4, matches 3")
	allow[1].n = 3
	flagged(t, "the allowed spellings", providerStringBreaches(found, allow), 0, "")
}
