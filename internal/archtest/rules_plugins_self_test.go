package archtest

import "testing"

func TestRPluginFlagsAnInternalImport(t *testing.T) {
	t.Parallel()
	imports := []string{"context", module + "pkg/plugin/source", module + "plugins/slack/domain", module + "internal/domain/unit"}
	flagged(t, "a plugin package", rulePluginSDK("plugins/slack/store", imports), 1, "internal/domain/unit")
	flagged(t, "a GitHub plugin package", rulePluginSDK("plugins/github/store", imports), 2, "(R-plugin)")
	flagged(t, "another plugin's package", rulePluginSDK("plugins/github/store", []string{module + "plugins/slack/domain"}), 1, "plugins/slack/domain")
	own := []string{module + "plugins/github/pr", module + "plugins/github/api/apitest"}
	flagged(t, "a GitHub plugin package of its own plugin", rulePluginSDK("plugins/github/collect", own), 0, "")
	flagged(t, "the stand-in", rulePluginSDK(standinDir, imports), 2, "(R-plugin)")
	flagged(t, "a bundled plugin imports the stand-in", rulePluginSDK("plugins/acme", []string{module + standinDir}), 1, "(R-plugin)")
	flagged(t, "a host package", rulePluginSDK("internal/plugins/source", imports), 0, "")
}

func TestRHostFlagsAPlantedPluginImport(t *testing.T) {
	t.Parallel()
	ps := []pkg{
		{ImportPath: module + "internal/cli", Imports: []string{module + "internal/readmodel/prrow/ghreporttest"}},
		{ImportPath: module + "internal/readmodel/prrow/ghreporttest", Imports: []string{module + "plugins/github/row"}},
		{ImportPath: module + "internal/adapter/source/github/ghsourcetest", Imports: []string{module + "internal/core/viewstest"}},
		{ImportPath: module + "internal/core/viewstest", Imports: []string{module + "plugins/github"}},
	}
	only := testOnly(ps)
	flagged(t, "a test-only package", ruleHostPlugin("internal/core/viewstest", ps[3].Imports, only), 0, "")
	flagged(t, "a test package the host's code imports", ruleHostPlugin("internal/readmodel/prrow/ghreporttest", ps[1].Imports, only), 1, "(R-host)")
	flagged(t, "a host package", ruleHostPlugin("internal/cli", []string{module + "plugins/slack", module + standinDir, module + pluginsRoot, "embed"}, only), 2, "never links its code")
	flagged(t, "a plugin imports its own package", ruleHostPlugin("plugins/slack", []string{module + "plugins/slack/store"}, only), 0, "")
	flagged(t, "the root package imports code", ruleEmbedOnly([]string{"embed", module + "pkg/plugin"}), 1, "(R-host)")
}

func TestStandInFlagsANameOutsideItsDirectory(t *testing.T) {
	t.Parallel()
	const home = "internal/archtest/testdata/acme"
	files := map[string][]byte{
		home + "/plugin.toml":              []byte(`id = "acme"`),
		standinTest:                        []byte(`"acme"`),
		"internal/intent/builtin/tools.go": []byte(`{"acme_note"}`),
		"internal/cli/serve.go":            []byte(`provider == "acme"`),
		"docs/rules.md":                    []byte("an acmes plugin, in " + home),
		"internal/config/acmes.go":         []byte("acmeX"),
	}
	flagged(t, "planted", ruleNamedOnlyAtHome(files, "acme", home, standinTest), 2, "names the acme plugin")
}
