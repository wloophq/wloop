package cli

import (
	"context"
	"io/fs"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/internal/profile"
)

// readBundled is rel, a slash path, as the bundled profile and the bundled
// plugins' layers hold it.
func readBundled(rel string) ([]byte, error) {
	r, err := profile.Resolve(profile.BundledChain(), rel)
	return r.Body, err
}

// bundledUI is what the bundled plugins' manifests and layers show of them.
func bundledUI(id unit.ProviderID) (*api.PluginUI, fs.FS) {
	return psource.BundledCatalog(string(id)).PluginUI(), nil
}

// adoptedByPlugin says whether the GitHub plugin reports ref taken over.
func adoptedByPlugin(t *testing.T, te *testEnv, ref pr.Ref) bool {
	t.Helper()
	data, err := te.reportedPRs().Data(context.Background(), []unit.Unit{ref.Unit()})
	if err != nil {
		t.Fatal(err)
	}
	return data[ref.Unit()].Adopted
}
