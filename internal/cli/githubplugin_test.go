package cli

import (
	"context"
	"encoding/json"
	"io"
	"slices"
	"sync"
	"testing"
	"time"

	ghpr "github.com/wloophq/wloop/plugins/github/pr"

	factsgithub "github.com/wloophq/wloop/internal/adapter/facts/github"
	ghsource "github.com/wloophq/wloop/internal/adapter/source/github"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/plugins/bundled"
	"github.com/wloophq/wloop/internal/plugins/host"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/internal/readmodel/prrow/ghreporttest"
	"github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
	"github.com/wloophq/wloop/pkg/plugin/plugintest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/pkg/plugin/source/sourcetest"
	ghplugin "github.com/wloophq/wloop/plugins/github"
	"github.com/wloophq/wloop/plugins/github/api/apitest"
	ghplugingh "github.com/wloophq/wloop/plugins/github/gh"
)

// bundledGitHubSource is the bundled GitHub plugin's [source] table.
func bundledGitHubSource(t *testing.T) *manifest.Source {
	t.Helper()
	srcs, err := bundled.Sources()
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(srcs, func(s *manifest.Source) bool { return s.Provider == string(unit.GitHub) })
	if i < 0 {
		t.Fatal("no bundled github manifest")
	}
	return srcs[i]
}

var githubPlugins sync.Map // *testEnv -> *src.Client

// githubPluginOf is the real GitHub plugin, run in-process over te's fake
// gh, with its broker over te's store: it records its events there and
// the store keeps the pull requests it reports, as serve's broker does.
func (te *testEnv) githubPluginOf() *src.Client {
	if c, ok := githubPlugins.Load(te); ok {
		return c.(*src.Client)
	}
	t := te.t
	t.Helper()
	b := &psource.Broker{ID: string(unit.GitHub), Manifest: bundledGitHubSource(t), Units: psource.Units{Codecs: te.unitCodecs()},
		Snapshots: psource.NewSnapshots(te.db.Snapshots()), Config: func() config.Config { return te.cfg },
		Events: sqlite.New(te.db), Marks: te.db, Drafts: te.db, Overrides: te.db, Workspaces: te.Env.workspaceListing,
		Paused: te.Env.roundsPaused, Rounds: te.Env.storedRoundState, Books: sqlite.New(te.db).Books,
		Hosted: &psource.Hosted{Profile: pluginProfile(string(unit.GitHub), te.Env.currentConfig)}}
	b = te.Env.withGitHubBroker(b, string(unit.GitHub), te.db)
	now := te.Now
	c := sourcetest.Launch(t, string(unit.GitHub), &ghplugin.Plugin{API: apitest.Options(quietGH{te.gh}), GH: quietGH{te.gh}, Now: func() time.Time { return now() },
		Git: ghreporttest.GitOf(factsgithub.LocalGit(te.Env.Git), te.Env.Git)}, b.Host())
	githubPlugins.Store(te, c)
	t.Cleanup(func() { githubPlugins.Delete(te) })
	return c
}

// syncInventory has the GitHub plugin read every pull request of te's
// fake GitHub in full, which te's store then keeps, outside ctx's meter
// scope and the fake's calls.
func (te *testEnv) syncInventory(context.Context) error {
	ctx := context.Background()
	te.gh.mu.Lock()
	viewer, _, order := te.gh.inventoryNodes()
	te.gh.mu.Unlock()
	if err := te.Store.SetViewer(ctx, unit.GitHub, viewer); err != nil {
		return err
	}
	c := te.githubPluginOf()
	for _, key := range order {
		ref, err := pr.ParseKey(key)
		if err != nil {
			return err
		}
		if _, err := c.Action(ctx, src.ActionRequest{Unit: psource.Wire(ref.Unit()), Action: ghsource.ActionRefresh}); err != nil {
			return err
		}
	}
	return nil
}

// linkWorkspaces runs the GitHub plugin's fast pass over te's workspaces,
// which links each to its pull request in te's store as the daemon's
// ingestion does.
func linkWorkspaces(t *testing.T, te *testEnv) {
	t.Helper()
	te.Store.UseClock(te.Now)
	hosted := te.gh.hosted
	te.gh.hosted = true
	defer func() { te.gh.hosted = hosted }()
	if _, err := te.githubPluginOf().Ingest(context.Background(), src.IngestRequest{Job: githubJobFast}); err != nil {
		t.Fatal(err)
	}
}

// testForge is the GitHub plugin as te's commands call it: its reads in
// full by the real plugin over te's fake gh, its viewer, basics and
// commands as the host reads them without one.
type testForge struct{ te *testEnv }

// Identity is the viewer the fake GitHub names, read as the plugin reads it.
func (f testForge) Identity(ctx context.Context) (src.Identity, error) {
	login, err := apitest.Runner(f.te.gh).Viewer(ctx)
	if err != nil {
		return src.Identity{}, err
	}
	return src.Identity{Viewer: src.Person{Key: string(unit.GitHub) + ":" + login, Name: login}}, nil
}

// Action reads the basics from the fake GitHub as the plugin does; the
// actions that keep plugin state run in the in-process plugin.
func (f testForge) Action(ctx context.Context, req src.ActionRequest) (json.RawMessage, error) {
	if req.Action != ghsource.ActionBasics {
		return f.te.githubPluginOf().Action(ctx, req)
	}
	ref, err := ghpr.ParseKey(req.Unit.Key)
	if err != nil {
		return nil, err
	}
	basics, err := apitest.Runner(f.te.gh).Basics(ctx, ref)
	if err != nil {
		return nil, err
	}
	return json.Marshal(basics)
}

func (f testForge) View(ctx context.Context, req src.ViewRequest) (json.RawMessage, error) {
	if c, err := f.te.Env.githubPlugin(); err == nil {
		return c.View(ctx, req)
	}
	return f.te.githubPluginOf().View(ctx, req)
}

func (f testForge) RunCLI(ctx context.Context, req src.CLIRequest, stdout, stderr io.Writer) (int, error) {
	return (&ghplugin.Plugin{GH: f.te.gh}).RunCLI(ctx, req, stdout, stderr)
}

// serveGitHubPlugin runs the real GitHub plugin as serve's own plugin, over
// db, until the test ends: its API over te's fake GitHub, its gh commands
// run with ghs, te's fake gh when nil.
func (te *testEnv) serveGitHubPlugin(db *sqlite.DB, ghs ghplugingh.Runner) {
	t := te.t
	t.Helper()
	te.plugins = []host.Plugin{{Manifest: bundledGitHubManifest(t), Dir: t.TempDir(), Binary: brand.PluginBinary(string(unit.GitHub)), Bundled: true}}
	l := plugintest.NewLauncher(t)
	now := te.Now
	if ghs == nil {
		ghs = te.gh
	}
	l.Add(string(unit.GitHub), map[plugin.Kind]plugin.Impl{src.Kind: src.New(&ghplugin.Plugin{API: apitest.Options(quietGH{te.gh}), GH: ghs,
		Now: func() time.Time { return now() }})})
	te.pluginLauncher = l
	ctx, cancel := context.WithCancel(te.serving(context.Background()))
	stop := te.servePlugins(ctx, db, te.cfg, t.Logf)
	t.Cleanup(func() {
		cancel()
		stop()
	})
	eventually(t, "the GitHub plugin loaded", func() bool { return te.Env.budget.last.Load() != nil })
}

// bundledGitHubManifest is the GitHub plugin's bundled manifest.
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

// ghForge is the GitHub plugin's commands run with gh, as a command line
// process with no running plugin would run them through its binary.
type ghForge struct{ gh ghplugingh.Runner }

func (ghForge) Identity(context.Context) (src.Identity, error) {
	return src.Identity{}, src.ErrUnavailable
}

func (ghForge) Action(context.Context, src.ActionRequest) (json.RawMessage, error) {
	return nil, src.ErrUnavailable
}

func (ghForge) View(context.Context, src.ViewRequest) (json.RawMessage, error) {
	return nil, src.ErrUnavailable
}

func (f ghForge) RunCLI(ctx context.Context, req src.CLIRequest, stdout, stderr io.Writer) (int, error) {
	return (&ghplugin.Plugin{GH: f.gh}).RunCLI(ctx, req, stdout, stderr)
}
