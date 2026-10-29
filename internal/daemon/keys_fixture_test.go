package daemon

import (
	"encoding/json"
	"flag"
	"os"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/keys"
	psource "github.com/wloophq/wloop/internal/plugins/source"
)

var updateKeys = flag.Bool("update-keys", false, "rewrite internal/web/testdata/keys.web.json")

// webKeysFixture is the cockpit's fixture of GET /api/keys?surface=web.
const webKeysFixture = "../web/testdata/keys.web.json"

// TestTheWebKeysFixtureIsWhatTheDaemonServes holds the cockpit's keys
// fixture to the keys the daemon serves on the web with the bundled profile
// and the bundled plugins enabled; -update-keys rewrites it.
func TestTheWebKeysFixtureIsWhatTheDaemonServes(t *testing.T) {
	s := &Server{}
	l := s.loaded()
	views := s.servedViews()
	var plugins []api.Plugin
	for _, p := range []struct {
		id   unit.ProviderID
		name string
	}{{unit.GitHub, "GitHub"}, {"slack", "Slack"}} {
		plugins = append(plugins, api.Plugin{ID: p.id, Name: p.name, Enabled: true, UI: psource.BundledCatalog(string(p.id)).PluginUI()})
	}
	got, err := json.MarshalIndent(api.KeysResponse{Bindings: keyViews(keys.Web, views, plugins, s.servedSpecs(), catalog.PlanSpecs(l),
		coreWords{l.Keys, l.Tasks})}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	if *updateKeys {
		if err := os.WriteFile(webKeysFixture, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(webKeysFixture)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("%s is stale: go test ./internal/daemon -run TestTheWebKeysFixtureIsWhatTheDaemonServes -update-keys rewrites it", webKeysFixture)
	}
}
