package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ghsource "github.com/wloophq/wloop/internal/adapter/source/github"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/source"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func githubDisabled(cfg *config.Config) {
	off := false
	cfg.Plugins = map[string]config.Plugin{string(unit.GitHub): {Enabled: &off}}
}

// countedGitHub is the github plugin counting its builds, with a stream,
// a job and a health so a test sees whether they are wired.
func countedGitHub(builds *atomic.Int32) source.Plugin {
	return source.Plugin{ID: unit.GitHub, Codec: pr.Codec{}, Build: func(cfg config.Config, sd source.Deps) (source.Module, error) {
		builds.Add(1)
		m, err := ghsource.Module(cfg, sd)
		m.Run = func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
		m.Jobs = []source.Job{{Name: "extract:github", Every: time.Hour, Run: func(context.Context) (string, string, error) { return "", "", nil }}}
		m.Health = func(context.Context) (api.SourceHealth, error) { return api.SourceHealth{}, nil }
		return m, err
	}}
}

func TestDisabledPluginIsNotBuilt(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	te.cfg.Brief.Enabled = true
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var builds atomic.Int32
	saved := plugins
	plugins = []source.Plugin{countedGitHub(&builds)}
	defer func() { plugins = saved }()

	on := serveDeps(context.Background(), te.Env, db, te.cfg, serveLog(t), &sync.Mutex{}, nil)
	if builds.Load() != 1 || len(on.Ingest) != 1 || len(on.RowProviders) != 1 || on.Collector == nil || len(on.Runtimes) != 1 || on.PluginEnabled != nil ||
		on.Intakes == nil || on.Explorer == nil || on.Budget == nil || on.Viewer == nil || te.Env.viewFacts().p.Load() == nil {
		t.Fatalf("github enabled: builds %d, deps %+v", builds.Load(), on)
	}

	githubDisabled(&te.cfg)
	d := serveDeps(context.Background(), te.Env, db, te.cfg, serveLog(t), &sync.Mutex{}, nil)
	if builds.Load() != 1 {
		t.Fatalf("a disabled plugin was built")
	}
	if len(d.Ingest) != 0 || len(d.RowProviders) != 0 || d.Collector != nil || d.Track != nil || len(d.Runtimes) != 0 {
		t.Fatalf("a disabled plugin's jobs: ingest %d, rows %d, collector %v, runtimes %d", len(d.Ingest), len(d.RowProviders), d.Collector, len(d.Runtimes))
	}
	if d.Brief != nil || d.BriefStatus != nil || d.Intakes != nil || d.IntakeBriefs != nil || d.Explorer != nil || d.Budget != nil || d.Viewer != nil {
		t.Fatalf("github's handles stayed: %+v", d)
	}
	if te.Env.viewFacts().p.Load() != nil {
		t.Fatal("the views kept a disabled plugin's workflow facts")
	}
	if u, err := d.Units.Parse("github", "acme/api#7"); err != nil || u != r7.Unit() {
		t.Fatalf("the codec of a disabled plugin: %v %v", u, err)
	}
	if d.PluginEnabled == nil || d.PluginEnabled(unit.GitHub) || !d.PluginEnabled("session") {
		t.Fatal("PluginEnabled does not say github is off")
	}
	s := daemon.New(d)
	if s.RunJob("ingest:github") || s.RunJob("slow") || s.RunJob("extract:github") || s.RunJob("brief") {
		t.Fatal("a disabled plugin's job runs")
	}
}

func TestPluginsRoute(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	plugin := func() api.Plugin {
		d := serveDeps(context.Background(), te.Env, db, te.cfg, serveLog(t), &sync.Mutex{}, nil)
		d.Startup = nil
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", api.Plugins, nil)
		req.Header.Set(api.Header, api.Version)
		daemon.New(d).Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
		if strings.Contains(rec.Body.String(), `"ui"`) && strings.Contains(rec.Body.String(), `"enabled":false`) {
			t.Fatalf("a disabled plugin's ui: %s", rec.Body)
		}
		var ps []api.Plugin
		if err := json.Unmarshal(rec.Body.Bytes(), &ps); err != nil || len(ps) != len(plugins) || ps[0].ID != unit.GitHub {
			t.Fatalf("%v %s", err, rec.Body)
		}
		return ps[0]
	}
	if got := plugin(); !got.Enabled {
		t.Fatalf("github enabled: %+v", got)
	}
	githubDisabled(&te.cfg)
	if got := plugin(); got.Enabled || got.UI != nil {
		t.Fatalf("github disabled: %+v", got)
	}
}

func TestPluginsDefaultEnabled(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"brief": {"enabled": false}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range plugins {
		if !cfg.PluginEnabled(string(p.ID)) {
			t.Errorf("%s is off with no plugins key", p.ID)
		}
	}
	var built []source.Module
	for _, p := range plugins {
		built = append(built, source.Module{ID: p.ID})
	}
	if pluginSwitch(plugins, built, cfg) != nil {
		t.Error("a config with no plugins key switches a plugin off")
	}
	names := map[unit.ProviderID]string{}
	for _, p := range pluginList(plugins, built, cfg) {
		if !p.Enabled {
			t.Errorf("%s listed disabled", p.ID)
		}
		names[p.ID] = p.Name
	}
	if names[unit.GitHub] != "GitHub" || names["slack"] != "Slack" {
		t.Errorf("plugin names %v", names)
	}
}

func TestStatusNamesDisabledPlugins(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.Socket = serveJSON(t, map[string]any{"GET /api/health": api.Health{}, "GET /api/rows": api.RowsPage{Full: true, Rows: []api.Row{}},
		"GET " + api.Plugins: []api.Plugin{{ID: unit.GitHub}, {ID: "thread", Enabled: true}}})
	if code := te.run("status"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if got := te.err.String(); !strings.Contains(got, "disabled plugins: github\n") || strings.Contains(got, "thread") {
		t.Fatalf("stderr:\n%s", got)
	}
	te.err.Reset()
	te.Socket = serveJSON(t, map[string]any{"GET /api/health": api.Health{}, "GET /api/rows": api.RowsPage{Full: true, Rows: []api.Row{}}})
	if code := te.run("status"); code != 0 || strings.Contains(te.err.String(), "disabled plugins") {
		t.Fatalf("a daemon with every plugin on: %d %s", code, te.err)
	}
}

func TestDaemonRunsWithNoPluginEnabled(t *testing.T) {
	te := newServeEnv(t)
	seedR7(t, te)
	var builds atomic.Int32
	saved := plugins
	plugins = []source.Plugin{countedGitHub(&builds)}
	defer func() { plugins = saved }()
	githubDisabled(&te.cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runServe(ctx, te.Env, []string{"--mcp-port", strconv.Itoa(freePort(t))}) }()
	c := client.New(te.Socket)
	h := waitHealth(t, te.Env, "the daemon up", func(api.Health) bool { return true })
	for _, name := range jobNames(h) {
		if strings.Contains(name, "github") || slices.Contains([]string{"fast", "slow", "brief"}, name) {
			t.Errorf("a source job runs: %v", jobNames(h))
		}
	}
	rows, err := c.Rows(ctx)
	if err != nil || len(rows) != 0 {
		t.Errorf("rows %v %v", rows, err)
	}
	ps, err := c.Plugins(ctx)
	if err != nil || len(ps) != 1 || ps[0].Enabled {
		t.Errorf("plugins %+v %v", ps, err)
	}
	if _, err := c.Action(ctx, r7.Unit(), "review", api.ActionRequest{Source: "cli"}); err == nil || !strings.Contains(err.Error(), "github is disabled") {
		t.Errorf("an action on a disabled plugin: %v", err)
	}
	if err := c.IntakeCancel(ctx, 1); err == nil || !strings.Contains(err.Error(), "github is disabled") {
		t.Errorf("an intake cancel with github disabled: %v", err)
	}
	if err := c.IntakeWithout(ctx, 1); err == nil || !strings.Contains(err.Error(), "github is disabled") {
		t.Errorf("an intake without with github disabled: %v", err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("serve returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not stop")
	}
	if builds.Load() != 0 {
		t.Fatalf("github was built %d times", builds.Load())
	}
}

func TestRuntimeCarriesJobTimeout(t *testing.T) {
	t.Parallel()
	rt := runtimeOf(source.Module{ID: unit.GitHub, Jobs: []source.Job{{Name: "j", Every: time.Minute, Timeout: 25 * time.Minute}}})
	if len(rt.Jobs) != 1 || rt.Jobs[0].Timeout != 25*time.Minute {
		t.Fatalf("jobs %+v", rt.Jobs)
	}
}

func TestPluginsServeWhetherTheyReact(t *testing.T) {
	const linear, wiki unit.ProviderID = "linear", "wiki"
	ps := []source.Plugin{{ID: linear, Name: "Linear"}, {ID: wiki, Name: "Wiki"}, {ID: unit.GitHub, Name: "GitHub"}}
	mods := []source.Module{{ID: linear, Reacts: true}, {ID: unit.GitHub}}
	off := false
	cfg := config.Config{Plugins: map[string]config.Plugin{string(wiki): {Enabled: &off}}}
	got := map[unit.ProviderID]bool{}
	for _, p := range pluginList(ps, mods, cfg) {
		got[p.ID] = p.Reacts
	}
	if want := map[unit.ProviderID]bool{linear: true, wiki: false, unit.GitHub: false}; !reflect.DeepEqual(got, want) {
		t.Fatalf("reacts %v, want %v", got, want)
	}
}
