package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/ingest"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/internal/source"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// servedUI is what the daemon of cfg serves on GET /api/plugins and GET
// /api/catalog.
func servedUI(t *testing.T, cfg func(*config.Config)) ([]api.Plugin, api.CatalogResponse) {
	t.Helper()
	te := newTestEnv(t, &fake.Manager{})
	if cfg != nil {
		cfg(&te.cfg)
	}
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	d := serveDeps(context.Background(), te.Env, db, te.cfg, t.Logf, &sync.Mutex{}, nil)
	d.Startup = nil
	h := daemon.New(d).Handler()
	read := func(path string, out any) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set(api.Header, api.Version)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body)
		}
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
	}
	var ps []api.Plugin
	var cat api.CatalogResponse
	read(api.Plugins, &ps)
	read(api.Catalog, &cat)
	return ps, cat
}

func pluginOf(ps []api.Plugin, id unit.ProviderID) api.Plugin {
	for _, p := range ps {
		if p.ID == id {
			return p
		}
	}
	return api.Plugin{}
}

func TestGitHubViewsServedAsPluginViews(t *testing.T) {
	ps, cat := servedUI(t, nil)
	gh := pluginOf(ps, unit.GitHub)
	if gh.UI == nil {
		t.Fatalf("github serves no ui: %+v", gh)
	}
	var want []string
	specs := map[string]bool{}
	for _, v := range cat.Views {
		specs[v.ID] = true
		if v.Provider == string(unit.GitHub) {
			want = append(want, v.ID)
		}
	}
	if len(want) == 0 {
		t.Fatal("the catalog serves no github view")
	}
	var got []string
	for _, sv := range gh.UI.Views {
		if !specs[sv.View] {
			t.Errorf("saved view %s names %q, which the catalog does not serve", sv.ID, sv.View)
		}
		if sv.Label == "" {
			t.Errorf("saved view %s has no label", sv.ID)
		}
		got = append(got, sv.View)
	}
	if len(got) != len(want) {
		t.Fatalf("saved views %v, catalog views %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("saved views %v, catalog views %v", got, want)
		}
	}
	for _, id := range []string{"plan", "dash", "explore", "recent", api.ViewAll, api.ViewLoop} {
		if !specs[id] {
			t.Errorf("view %s is not served", id)
		}
	}
	for _, sec := range gh.UI.Menubar {
		if !specs[sec.View] {
			t.Errorf("menu bar section %s names %q, which the catalog does not serve", sec.Label, sec.View)
		}
	}
	if len(gh.UI.Menubar) == 0 {
		t.Errorf("github declares no menu bar section: %+v", gh)
	}
}

func TestDisabledPluginHasNoViews(t *testing.T) {
	ps, cat := servedUI(t, githubDisabled)
	if gh := pluginOf(ps, unit.GitHub); gh.Enabled || gh.UI != nil {
		t.Fatalf("disabled github: %+v", gh)
	}
	for _, v := range cat.Views {
		if v.Provider == string(unit.GitHub) {
			t.Errorf("the catalog serves github's view %s", v.ID)
		}
	}
	for _, f := range cat.Facets {
		if f.Provider == string(unit.GitHub) {
			t.Errorf("the catalog serves github's facet %s", f.ID)
		}
	}
}

func TestPluginWithCoreKeyIsNotLoaded(t *testing.T) {
	keyed := source.Plugin{ID: unit.GitHub, Codec: pr.Codec{}, Build: func(config.Config, source.Deps) (source.Module, error) {
		return source.Module{ID: unit.GitHub, Codec: pr.Codec{},
			Ingest: ingest.Job{Every: time.Minute, Prepare: func(context.Context, time.Time) (ingest.Plan, string, error) { return ingest.Plan{}, "", nil }},
			Rows:   func(readmodel.RowDeps) readmodel.Provider { return nil },
			UI:     &api.PluginUI{Keys: []api.KeyBinding{{Key: "x", Action: "pick", Surface: "web"}}}}, nil
	}}
	mods, err := buildSources([]source.Plugin{keyed}, config.Config{}, source.Deps{})
	if len(mods) != 0 || err == nil {
		t.Fatalf("a plugin taking the core key x loaded: %d modules, %v", len(mods), err)
	}
	for _, w := range []string{"github", "x", "web"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("%q does not name %q", err, w)
		}
	}
	on := pluginSwitch([]source.Plugin{keyed}, mods, config.Config{})
	if on == nil || on(unit.GitHub) {
		t.Error("a plugin refused at build is switched on")
	}
	if ps := pluginList([]source.Plugin{keyed}, mods, config.Config{}); len(ps) != 1 || ps[0].Enabled {
		t.Errorf("a plugin refused at build is listed %+v", ps)
	}
}
