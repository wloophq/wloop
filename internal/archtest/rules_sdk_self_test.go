package archtest

import "testing"

func TestRSDKFlagsAnInternalImport(t *testing.T) {
	t.Parallel()
	imports := []string{"context", module + "pkg/plugin", module + "internal/domain/unit", "github.com/acme/internal/x"}
	flagged(t, "an SDK package", ruleSDK("pkg/plugin/source", imports), 1, "internal/domain/unit")
	flagged(t, "a host package", ruleSDK("internal/plugins/host", imports), 0, "")
}

func TestRPluginFlagsAnInternalImport(t *testing.T) {
	t.Parallel()
	imports := []string{"context", module + "pkg/plugin/source", module + "plugins/slack/domain", module + "internal/domain/unit"}
	flagged(t, "a plugin package", rulePluginSDK("plugins/slack/store", imports), 1, "internal/domain/unit")
	flagged(t, "a GitHub plugin package", rulePluginSDK("plugins/github/store", imports), 2, "(R-plugin)")
	flagged(t, "another plugin's package", rulePluginSDK("plugins/github/store", []string{module + "plugins/slack/domain"}), 1, "plugins/slack/domain")
	own := []string{module + "plugins/github/pr", module + "plugins/github/api/apitest"}
	flagged(t, "a GitHub plugin package of its own plugin", rulePluginSDK("plugins/github/collect", own), 0, "")
	flagged(t, "the bundled manifests", rulePluginSDK("plugins", imports), 0, "")
	flagged(t, "a host package", rulePluginSDK("internal/plugins/source", imports), 0, "")
}
