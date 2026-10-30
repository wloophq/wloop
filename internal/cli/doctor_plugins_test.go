package cli

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	goplugin "github.com/hashicorp/go-plugin"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/plugins/host"
	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/pkg/plugin/source/sourcetest"
)

// pluginChildEnv makes this test binary serve a source plugin when it is
// started as one: at the SDK's protocol, or at the protocol it names.
const pluginChildEnv = "CLI_TEST_PLUGIN_PROTOCOL"

// servedAsPlugin serves a plugin when this test binary was started as one.
func servedAsPlugin() bool {
	proto := os.Getenv(pluginChildEnv)
	if proto == "" {
		return false
	}
	impls := map[plugin.Kind]plugin.Impl{src.Kind: src.New(sourcetest.NewStub())}
	if proto == fmt.Sprint(plugin.Protocol) {
		plugin.Serve(impls)
		return true
	}
	var n int
	fmt.Sscan(proto, &n)
	goplugin.Serve(&goplugin.ServeConfig{HandshakeConfig: plugin.Handshake, GRPCServer: goplugin.DefaultGRPCServer,
		VersionedPlugins: map[int]goplugin.PluginSet{n: plugin.VersionedPlugins(impls)[plugin.Protocol]}})
	return true
}

// placePlugin writes plugin id under root with this test binary as its
// binary, which serves at protocol; it returns the binary's checksum.
func placePlugin(t *testing.T, root, id string, manifestProtocol int) []byte {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.toml"), []byte(acmeManifest(id, manifestProtocol)), 0o644); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(self, filepath.Join(dir, brand.PluginBinary(id))); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	return sum[:]
}

// pluginsDoctor is a test env whose config, state and binary directories
// hold plugins: user's under the config directory, bundled ones beside the
// binary.
func pluginsDoctor(t *testing.T) (te *testEnv, user, bundled string) {
	te = newTestEnv(t, nil)
	user = filepath.Join(filepath.Dir(te.ConfigPath), "plugins")
	te.Executable = filepath.Join(t.TempDir(), brand.Name)
	bundled = filepath.Join(filepath.Dir(te.Executable), "plugins")
	return te, user, bundled
}

func checkOf(t *testing.T, checks []check, name string) check {
	t.Helper()
	for _, c := range checks {
		if c.name == name {
			return c
		}
	}
	t.Fatalf("no check %q in %+v", name, checks)
	return check{}
}

func TestDoctorShowsEachPluginsTrust(t *testing.T) {
	t.Setenv(pluginChildEnv, fmt.Sprint(plugin.Protocol))
	te, user, bundled := pluginsDoctor(t)
	placePlugin(t, user, "acme", 1)
	placePlugin(t, user, "delta", 1)
	placePlugin(t, bundled, "beta", 1)
	sum := placePlugin(t, bundled, "gamma", 1)
	if err := os.MkdirAll(filepath.Dir(host.ChecksumsFile(te.StateDir)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(host.ChecksumsFile(te.StateDir), fmt.Appendf(nil, "%x  gamma\n", sum), 0o600); err != nil {
		t.Fatal(err)
	}
	te.cfg.Plugins.Other = map[string]config.Plugin{"delta": {Trust: host.TrustPath}}

	checks := te.pluginChecks(t.Context(), te.cfg, &api.Health{Plugins: []api.PluginHealth{{ID: "gamma", Status: "serving"}, {ID: "delta", Status: "down", Reason: "it crashed"}}})
	acme := checkOf(t, checks, "plugin acme")
	if acme.mark != markMissing || !strings.Contains(acme.info, "which no install recorded") || !strings.Contains(acme.fix, `[plugins.acme] trust = "path"`) {
		t.Errorf("an unrecorded user binary: %+v", acme)
	}
	beta := checkOf(t, checks, "plugin beta")
	if beta.mark != markMissing || !strings.Contains(beta.info, "whose checksum no install recorded") || beta.fix != brand.Command+" install" {
		t.Errorf("a bundled binary no install recorded: %+v", beta)
	}
	gamma := checkOf(t, checks, "plugin gamma")
	if gamma.mark != markOK || !strings.Contains(gamma.info, fmt.Sprintf("sha256 %x", sum[:6])) || !strings.Contains(gamma.info, "trusted by its recorded checksum, protocol 1, serving") {
		t.Errorf("a recorded bundled binary: %+v", gamma)
	}
	delta := checkOf(t, checks, "plugin delta")
	if delta.mark != markMissing || !strings.Contains(delta.info, "trusted by its path, protocol 1, down in the daemon: it crashed") {
		t.Errorf("a binary trusted by its path, down in the daemon: %+v", delta)
	}
}

func TestDoctorReportsAPluginWithTheWrongProtocol(t *testing.T) {
	t.Setenv(pluginChildEnv, fmt.Sprint(plugin.Protocol+1))
	te, user, _ := pluginsDoctor(t)
	placePlugin(t, user, "acme", plugin.Protocol)
	placePlugin(t, user, "old", plugin.Protocol+1)
	te.cfg.Plugins.Other = map[string]config.Plugin{"acme": {Trust: host.TrustPath}, "old": {Trust: host.TrustPath}}

	checks := te.pluginChecks(t.Context(), te.cfg, nil)
	acme := checkOf(t, checks, "plugin acme")
	if acme.mark != markMissing || !strings.Contains(acme.info, fmt.Sprintf("it speaks another plugin protocol than this release's %d", plugin.Protocol)) ||
		!strings.Contains(acme.fix, "built for this release") {
		t.Errorf("a binary speaking another protocol: %+v", acme)
	}
	manifests := checkOf(t, checks, "plugin manifests")
	if manifests.mark != markMissing || !strings.Contains(manifests.info, fmt.Sprintf("protocol %d is not one this host speaks", plugin.Protocol+1)) {
		t.Errorf("a manifest declaring another protocol: %+v", manifests)
	}
}
