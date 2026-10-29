package cli

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory/inventorytest"

	"github.com/wloophq/wloop/internal/readmodel/prrow/ghreporttest"

	src "github.com/wloophq/wloop/pkg/plugin/source"

	"github.com/wloophq/wloop/internal/adapter/source/github/present"
	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/ingest"
	"github.com/wloophq/wloop/internal/core/ingest/ingesttest"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/core/lock"
	"github.com/wloophq/wloop/internal/core/port"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/card"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/wake"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// withRows is d with every inventory role on st and the sources' codecs and
// rows; it reads nothing from GitHub: no ingestion, slow job or track.
func withRows(d daemon.Deps, st *inventory.Store) daemon.Deps {
	cfg := d.Cfg
	now := d.Now
	if now == nil {
		now = st.Now
	}
	reports := ghreporttest.Inventory{Store: st, Now: now, Config: func() config.Config { return cfg }, Identity: d.Identity}
	mods, err := buildSources(plugins, d.Cfg, moduleDeps{Inventory: st, DB: st.DB(), Turns: d.Turns, Clock: clockFunc(time.Now), Reports: reports}, bundledUI)
	if err != nil {
		panic(err)
	}
	d = withSources(withStore(d, st), plugins, mods, bundledUI)
	d = withGitHub(d, present.Deps{Store: st, Reports: reports, Host: moduleBroker(d.Units, st.DB()),
		Mutes: readmodel.WakeMutes(st.DB().Wakes().UnitWakes)})
	d.Ingest, d.Collector = nil, nil
	d.Follows = func(p unit.ProviderID) bool { return p == unit.GitHub }
	d.Track = func(_ context.Context, u unit.Unit) (port.TrackRead, error) {
		return port.TrackRead{Unit: u, Save: func(ctx context.Context) error { return setTracked(ctx, st, u, true) }}, nil
	}
	d.Untrack = func(ctx context.Context, u unit.Unit) error { return setTracked(ctx, st, u, false) }
	if d.Forge == "" {
		d.Forge = unit.GitHub
	}
	return d
}

// setTracked tracks or untracks u in st, as the GitHub plugin's follow
// reports it.
func setTracked(ctx context.Context, st *inventory.Store, u unit.Unit, tracked bool) error {
	ref, err := pr.ParseKey(u.Key)
	if err != nil {
		return err
	}
	_, err = inventorytest.Of(st).SetTracked(ctx, ref, tracked)
	return err
}

// threadRows serves a row for each of units, titled by title or else by
// its key.
type threadRows struct {
	units []unit.Unit
	title func(unit.Unit) string
}

func (threadRows) Provider() unit.ProviderID { return ingesttest.Provider }

func (p threadRows) Listed(context.Context) ([]unit.Unit, error) { return p.units, nil }

func (p threadRows) Load(_ context.Context, units []unit.Unit) (map[unit.Unit]any, error) {
	in := map[unit.Unit]any{}
	for _, u := range units {
		in[u] = u.Key
		if p.title != nil {
			in[u] = p.title(u)
		}
	}
	return in, nil
}

func (threadRows) Derive(_ readmodel.Base, in any, _ time.Time) (api.Row, error) {
	return api.Row{Title: in.(string)}, nil
}

// threadModule is a fake plugin of the thread provider serving th's row.
func threadModule(th unit.Unit) compiledPlugin {
	return compiledPlugin{ID: ingesttest.Provider, Codec: ingesttest.Codec(), Build: func(_ config.Config, d moduleDeps) (pluginModule, error) {
		reg := ingest.Registration{Source: ingesttest.Source{C: ingesttest.NewClient(ingesttest.NewService("viewer"), 100)},
			Store: ingesttest.NewStore(d.Changes), Codec: ingesttest.Codec()}
		return pluginModule{
			ID:    ingesttest.Provider,
			Codec: ingesttest.Codec(),
			Ingest: ingest.Job{Every: time.Minute, Prepare: func(context.Context, time.Time) (ingest.Plan, string, error) {
				return ingest.Plan{Registration: reg}, "", nil
			}},
			Rows: func(readmodel.RowDeps) readmodel.Provider { return threadRows{units: []unit.Unit{th}} },
		}, nil
	}}
}

func TestAModuleRegistersItsCodecJobAndRows(t *testing.T) {
	th := unit.Unit{Provider: ingesttest.Provider, Key: "C1 0001"}
	te := newTestEnv(t, &fake.Manager{})
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	serve := func(ps []compiledPlugin) *daemon.Server {
		saved := plugins
		plugins = ps
		defer func() { plugins = saved }()
		d := te.serveDeps(context.Background(), db, te.cfg, t.Logf, &lock.Mutex{}, nil)
		d.Startup = nil
		return daemon.New(d)
	}
	detail := func(s *daemon.Server) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", api.UnitPath(th), nil)
		req.Header.Set(api.Header, api.Version)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		return rec
	}

	with := serve(append(slices.Clone(plugins), threadModule(th)))
	if !with.RunJob("ingest:" + string(ingesttest.Provider)) {
		t.Error("the thread module got no ingestion job")
	}
	if !with.RunJob("ingest:github") {
		t.Error("the github module lost its ingestion job")
	}
	if rec := detail(with); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"title":"C1 0001"`) {
		t.Errorf("the thread route: %d %s", rec.Code, rec.Body)
	}

	without := serve(plugins)
	if without.RunJob("ingest:" + string(ingesttest.Provider)) {
		t.Error("a removed module kept its ingestion job")
	}
	if rec := detail(without); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "no pull request") {
		t.Errorf("a removed module's route: %d %s", rec.Code, rec.Body)
	}
}

// carded is p's module with a card and a derived item per row.
func carded(p compiledPlugin) compiledPlugin {
	build := p.Build
	p.Build = func(cfg config.Config, d moduleDeps) (pluginModule, error) {
		m, err := build(cfg, d)
		m.Card = func(_ context.Context, r api.Row) (card.Card, error) {
			return card.Card{Unit: r.Unit, Title: r.Title, State: "thread"}, nil
		}
		m.Items = func(_ context.Context, r api.Row) ([]items.Item, error) {
			return []items.Item{{ID: "th:ask", Unit: r.Unit, Kind: items.Request, Summary: r.Title, Requester: items.Person{Key: "thread:U000AAA"},
				Origin: unit.Anchor{Unit: r.Unit, Ref: "m:1"}, State: items.Open, Confidence: items.High, Basis: items.NoBasis}}, nil
		}
		return m, err
	}
	return p
}

// liveThread is a fake thread plugin whose row title the test sets, with
// the change log its module was built with.
type liveThread struct {
	title   atomic.Pointer[string]
	changes atomic.Pointer[change.Log]
}

func (l *liveThread) plugin(th unit.Unit) compiledPlugin {
	p := threadModule(th)
	build := p.Build
	p.Build = func(cfg config.Config, d moduleDeps) (pluginModule, error) {
		m, err := build(cfg, d)
		l.changes.Store(d.Changes)
		m.Rows = func(readmodel.RowDeps) readmodel.Provider {
			return threadRows{units: []unit.Unit{th}, title: func(unit.Unit) string { return *l.title.Load() }}
		}
		return m, err
	}
	return carded(p)
}

func (l *liveThread) retitle(title string, u unit.Unit) {
	l.title.Store(&title)
	l.changes.Load().Note(port.TopicSourceEvents, u)
}

func serveNoJobs(t *testing.T, te *testEnv) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runServe(ctx, te.Env, []string{"--no-jobs", "--mcp-port", strconv.Itoa(freePort(t))}) }()
	waitHealth(t, te.Env, "the daemon up", func(api.Health) bool { return true })
	return func() {
		cancel()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Fatalf("serve returned %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("serve did not stop")
		}
	}
}

func openStateDB(t *testing.T, te *testEnv) *sqlite.DB {
	t.Helper()
	db, err := sqlitetest.Open(filepath.Join(te.StateDir, storeFile), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestCardsJobRunsUnderNoJobs(t *testing.T) {
	th := unit.Unit{Provider: ingesttest.Provider, Key: "C1 0001"}
	te := newServeEnv(t)
	live := &liveThread{}
	first := "first"
	live.title.Store(&first)
	saved := plugins
	plugins = append(slices.Clone(plugins), live.plugin(th))
	defer func() { plugins = saved }()
	stop := serveNoJobs(t, te)
	defer stop()
	db := openStateDB(t, te)
	ctx := context.Background()
	follows := func(title string) func() bool {
		return func() bool {
			got, err := db.Cards().Get(ctx, []unit.Unit{th})
			if err != nil || got[th].Card.Title != title {
				return false
			}
			its, err := db.Items().Load(ctx, []unit.Unit{th})
			return err == nil && len(its[th]) == 1 && its[th][0].Provenance == items.Derived && its[th][0].Summary == title
		}
	}
	eventually(t, "the thread's card and item", follows("first"))
	live.retitle("second", th)
	eventually(t, "the card and item following the changed row", follows("second"))
}

func TestCardsJobResolvesTheItemsOfALapsedPR(t *testing.T) {
	te := newServeEnv(t)
	ctx := context.Background()
	seed, err := sqlitetest.Open(filepath.Join(te.StateDir, storeFile), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := inventorytest.Of(inventory.New(seed)).UpsertPR(ctx, dinv.PR{Ref: r7, Title: "Batch export", Author: "ana", State: "OPEN", URL: r7.URL()}); err != nil {
		t.Fatal(err)
	}
	stale := items.Item{ID: "th:ask", Unit: r7.Unit(), Kind: items.Request, Summary: "review", Requester: items.Person{Key: "github:ana"},
		Origin: unit.Anchor{Unit: r7.Unit(), Ref: "review:1"}, State: items.Open, Confidence: items.High, Basis: items.NoBasis,
		Provenance: items.Derived, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := seed.Items().Commit(ctx, coreitems.Commit{Items: []items.Item{stale}}); err != nil {
		t.Fatal(err)
	}
	seed.Close()
	var builds atomic.Int32
	saved := plugins
	plugins = []compiledPlugin{carded(countedGitHub(&builds))}
	defer func() { plugins = saved }()
	stop := serveNoJobs(t, te)
	defer stop()
	db := openStateDB(t, te)
	eventually(t, "the lapsed PR's derived item resolved", func() bool {
		its, err := db.Items().Load(ctx, []unit.Unit{r7.Unit()})
		return err == nil && len(its[r7.Unit()]) == 1 && its[r7.Unit()][0].State == items.Resolved && its[r7.Unit()][0].Resolution == items.Superseded
	})
}

// viewerIs is the forge's Identity of login with teams.
func viewerIs(login string, teams ...string) func(context.Context) (src.Identity, error) {
	return func(context.Context) (src.Identity, error) { return identityOf(login, teams...), nil }
}

func identityOf(login string, teams ...string) src.Identity {
	return src.Identity{Viewer: src.Person{Key: "github:" + login, Name: login}, Teams: teams}
}

// moduleBroker is the broker of the GitHub module over db, as serve's
// moduleHost builds it, with no pause.
func moduleBroker(codecs *unit.Registry, db *sqlite.DB) src.Host {
	b := &psource.Broker{ID: string(unit.GitHub), Units: psource.Units{Codecs: codecs}, Events: sqlite.New(db), Marks: db, Drafts: db, Overrides: db,
		Wakes: prTaskWakes{db: db, svc: coretasks.New(coretasks.Deps{Store: db.Tasks(), States: taskstest.BundledStates, Wakes: db.Wakes(),
			Clock: clockFunc(time.Now)})}}
	return b.Host()
}

// prTaskWakes are db's tasks as the mute's wakes move them; a unit no open
// task holds gets one, in next, as each followed pull request has its task.
type prTaskWakes struct {
	db  *sqlite.DB
	svc *coretasks.Service
}

func (w prTaskWakes) TasksOf(ctx context.Context, u unit.Unit) ([]task.Task, error) {
	ts, err := w.db.Tasks().TasksOf(ctx, u)
	if err != nil || slices.ContainsFunc(ts, task.Task.Open) {
		return ts, err
	}
	now := time.Now()
	e := task.Task{ID: task.DerivedID(u, "pr"), State: "next", Category: task.CategoryReady, Units: []unit.Unit{u}, Origin: task.OriginUser,
		Created: now, Updated: now}
	return append(ts, e), w.db.Tasks().Apply(ctx, []task.Change{{Op: task.OpCreate, Task: e}})
}

func (w prTaskWakes) Sleep(ctx context.Context, id task.ID, k wake.Wake) error {
	return w.svc.Sleep(ctx, id, k)
}

func (w prTaskWakes) Rouse(ctx context.Context, id task.ID) error { return w.svc.Rouse(ctx, id) }
