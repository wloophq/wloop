package archtest

import "testing"

func TestRSDKFlagsAnInternalImport(t *testing.T) {
	t.Parallel()
	imports := []string{"context", module + "pkg/plugin", module + "internal/domain/unit", "github.com/acme/internal/x"}
	flagged(t, "an SDK package", ruleSDK("pkg/plugin/source", imports), 1, "internal/domain/unit")
	flagged(t, "a host package", ruleSDK("internal/plugins/host", imports), 0, "")
}
