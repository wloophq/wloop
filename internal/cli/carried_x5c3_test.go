package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/plugins/bundled"
	"github.com/wloophq/wloop/internal/plugins/host"
	"github.com/wloophq/wloop/internal/source"
	"github.com/wloophq/wloop/internal/workspace/fake"
	"github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
	"github.com/wloophq/wloop/pkg/plugin/plugintest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/pkg/plugin/source/sourcetest"
)

// forwardingStub is a GitHub plugin that answers every request it forwards
// with the viewer, keeping what it was asked to send.
type forwardingStub struct {
	*forgeStub
	mu   sync.Mutex
	sent []map[string]any
}

func (s *forwardingStub) Action(_ context.Context, req src.ActionRequest) (json.RawMessage, error) {
	if req.Action != "github.forward" {
		return nil, src.ErrUnsupported
	}
	var x map[string]any
	if err := json.Unmarshal(req.Args, &x); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.sent = append(s.sent, x)
	s.mu.Unlock()
	return json.Marshal(map[string]any{"status": 200, "header": map[string][]string{"Content-Type": {"application/json"}},
		"body": []byte(`{"data":{"viewer":{"login":"ana"}}}`)})
}

func TestServeHandsGitHubsAPIClientToNoInProcessModule(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	te.github = newGitHubClients()
	te.plugins = []host.Plugin{{Manifest: bundledGitHubManifest(t), Dir: t.TempDir(), Binary: brand.PluginBinary(string(unit.GitHub)), Bundled: true}}
	stub := &forwardingStub{forgeStub: &forgeStub{Stub: sourcetest.NewStub(), login: "ana"}}
	l := plugintest.NewLauncher(t)
	l.Add(string(unit.GitHub), map[plugin.Kind]plugin.Impl{src.Kind: src.New(stub)})
	te.pluginLauncher = l
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	stop := te.servePlugins(ctx, db, te.cfg, t.Logf)
	t.Cleanup(func() {
		cancel()
		stop()
	})
	eventually(t, "the plugin runs", func() bool { _, err := te.pluginKind.Client(string(unit.GitHub)); return err == nil })

	deps := te.withGitHubClient(source.Deps{})
	if deps.GitHub != te.github.API {
		t.Fatal("the in-process module got another client than serve's")
	}
	login, err := deps.GitHub.Viewer(ctx)
	if err != nil || login != "ana" {
		t.Fatalf("the viewer through the plugin: %q %v", login, err)
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if len(stub.sent) != 1 || stub.sent[0]["graphql"] != true || stub.sent[0]["method"] != "POST" {
		t.Fatalf("the plugin was asked to send %+v, want the one GraphQL read", stub.sent)
	}
	if h, _ := stub.sent[0]["header"].(map[string]any); h["Authorization"] != nil {
		t.Fatalf("the host sent a credential of its own: %v", h["Authorization"])
	}
}

// bundledGitHubManifest is the bundled GitHub plugin's manifest.
func bundledGitHubManifest(t *testing.T) *manifest.Manifest {
	t.Helper()
	ms, err := bundled.Manifests()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		if m.ID == string(unit.GitHub) {
			return m
		}
	}
	t.Fatal("no bundled github manifest")
	return nil
}
