package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/gitx"
	"github.com/wloophq/wloop/internal/plugins/host"
	"github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
	"github.com/wloophq/wloop/pkg/plugin/plugintest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/pkg/plugin/source/sourcetest"
)

// branchLocator is a source whose units are the branches its checkouts are on.
type branchLocator struct{ *sourcetest.Stub }

func (branchLocator) Locate(ctx context.Context, dir string) (plugin.Unit, bool, error) {
	out, err := exec.CommandContext(ctx, gitx.Binary(), "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return plugin.Unit{}, false, nil
	}
	branch, ok := strings.CutPrefix(strings.TrimSpace(string(out)), "acme/")
	if !ok {
		return plugin.Unit{}, false, nil
	}
	return plugin.Unit{Provider: "acme", Key: branch}, true, nil
}

func TestLocateResolvesAWorktree(t *testing.T) {
	repo, wt, plain := t.TempDir(), filepath.Join(t.TempDir(), "deploy"), t.TempDir()
	gitRun(t, repo, "init", "-q", "-b", "main")
	commitFile(t, repo, "README", "acme")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "acme/deploy", wt)
	gitRun(t, plain, "init", "-q", "-b", "main")

	te := newTestEnv(t, nil)
	m, err := manifest.Parse([]byte("id = \"acme\"\nkinds = [\"source\"]\nprotocol = 1\n[source]\nname = \"Acme\"\nslot = \"acme\"\ncodec = {keys = [\"{name}\"]}\n"))
	if err != nil {
		t.Fatal(err)
	}
	te.plugins = []host.Plugin{{Manifest: m, Dir: t.TempDir(), Binary: brand.PluginBinary("acme"), Bundled: true}}
	l := plugintest.NewLauncher(t)
	l.Add("acme", map[plugin.Kind]plugin.Impl{src.Kind: src.New(branchLocator{sourcetest.NewStub()})})
	te.pluginLauncher = l
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	stop := te.servePlugins(ctx, db.Snapshots(), te.cfg, t.Logf)
	t.Cleanup(func() {
		cancel()
		stop()
	})
	eventually(t, "the plugin runs", func() bool { _, err := te.pluginKind.Client("acme"); return err == nil })

	got, err := te.handUnit(ctx, "", filepath.Join(wt, "."), wsdomain.Workspace{}, wsdomain.Repo{}, false)
	if want := (unit.Unit{Provider: "acme", Key: "deploy"}); err != nil || got != want {
		t.Errorf("the worktree on acme/deploy holds %v (%v), want %v", got, err, want)
	}
	if got, err := te.handUnit(ctx, "", plain, wsdomain.Workspace{}, wsdomain.Repo{}, false); err == nil {
		t.Errorf("a checkout no plugin locates holds %v", got)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatal(err)
	}
}
