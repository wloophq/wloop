package cli

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/lock"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/daemon"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/gitx"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/plugins/bundled"
	"github.com/wloophq/wloop/internal/plugins/host"
	"github.com/wloophq/wloop/internal/workspace"
	"github.com/wloophq/wloop/internal/workspace/fake"
	"github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/plugintest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// TestOptions are what ServeForTest puts in place of the outside world in the daemon serve composes.
type TestOptions struct {
	// Publishers stand, by provider, for the Publisher of the plugin module serve builds, pinned to their turns
	// as serve pins a plugin's; one that is an outbox.Bases reads its units' live basis.
	Publishers map[unit.ProviderID]port.Effects
	// Now is the daemon's clock; nil is a fixed instant.
	Now func() time.Time
	// Config edits the default configuration the daemon serves.
	Config func(*config.Config)
	// Plugins run, by plugin id, as serve runs that bundled plugin's process, such as the GitHub plugin over a
	// fake GitHub; a plugin left out does not run.
	Plugins map[string]src.Source
}

// TestDaemon is a daemon serve composed over a temporary home, with no collector, sweep loop or sender of its
// own: a test runs them with Sweep and Send.
type TestDaemon struct {
	e      *Env
	client *client.Client
	ctx    context.Context
	loopMu *lock.Mutex
	handle atomic.Int64
}

// TestSession is an agent session that runs a turn: the handle it calls the daemon with, and the turn's id.
type TestSession struct {
	Handle, Round string
	Unit          unit.Unit
}

// ServeForTest serves, until t ends, the daemon's composition root over a store in a temporary directory, the
// plugins' publishers replaced by o.Publishers, and returns it.
func ServeForTest(t testing.TB, o TestOptions) *TestDaemon {
	t.Helper()
	dir, err := os.MkdirTemp("", "wl-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	now := o.Now
	if now == nil {
		at := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
		now = func() time.Time { return at }
	}
	logf := func(format string, args ...any) { t.Logf(format, args...) }
	e := (&Env{
		Out: servedLog{t}, Err: servedLog{t},
		Git:        gitx.Git{R: servedNoGit{}},
		WS:         workspace.NewSet(&fake.Manager{}),
		Sh:         func(context.Context, string, ...string) ([]byte, error) { return nil, errNoShell },
		Stdout:     func(context.Context, string, ...string) ([]byte, error) { return nil, errNoShell },
		ConfigPath: filepath.Join(dir, "config.toml"),
		StateDir:   filepath.Join(dir, "state"),
		Home:       filepath.Join(dir, "home"),
		Socket:     filepath.Join(dir, "d.sock"),
		Now:        now,
		Getenv:     func(string) string { return "" },
		Harness:    harnessOf(nil, defaultManagers()),
		Agents:     testAgents(),
		Exit:       func(code int) { t.Errorf("exit %d", code) },
		Reexec:     func() error { return errors.New("no reexec in a test daemon") },

		NativeAvailable: func() bool { return false },
	}).owning()
	cfg := (&Env{}).defaultConfig()
	cfg.Agent, cfg.HTTP.Port, cfg.WorkspaceManagers = "echo", 0, []string{"fake"}
	if o.Config != nil {
		o.Config(&cfg)
	}
	e.LoadConfig = func() (config.Config, error) { return cfg, nil }
	e.intents = newEnvIntents(e)
	e.testPublishers = o.Publishers
	e.noJobs = true

	if err := os.MkdirAll(e.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.changes = change.NewLog(change.DefaultWindow, logf)
	db, unlockStore, err := e.openStore(ctx, logf)
	if err != nil || db == nil {
		cancel()
		t.Fatalf("opening the test daemon's store: %v", err)
	}
	store := inventory.New(db)
	store.UseClock(e.Now)
	e.Rec = storeRecorder{store}
	e.interrupted = new(atomic.Bool)
	e.linkStates = new(atomic.Pointer[daemon.Server])
	e.Rounds = sqlite.NewRounds(db)
	e.cfg = e.configHolder(cfg)
	e.meter = meter.NewRegistry(nil)
	stopPlugins := e.serveTestPlugins(t, ctx, db, cfg, o.Plugins, logf)
	loopMu := &lock.Mutex{}
	deps := serveDeps(ctx, e, db, cfg, logf, loopMu, &daemon.AppState{})
	deps.Startup, deps.StateDir = nil, e.StateDir
	deps.Socket, deps.LockPath, deps.InfoPath = e.Socket, filepath.Join(dir, daemonLock), filepath.Join(dir, httpInfoFile)
	srv := daemon.New(deps)
	e.Briefing, e.consequences = srv, srv
	e.linkStates.Store(srv)
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("the test daemon: %v", err)
		}
		srv.Wait()
		stopPlugins()
		e.background.Wait()
		db.Close()
		unlockStore()
	})
	d := &TestDaemon{e: e, client: client.New(e.Socket), ctx: ctx, loopMu: loopMu}
	deadline := time.Now().Add(time.Minute)
	for {
		if _, err := d.client.Health(ctx); err == nil {
			return d
		} else if time.Now().After(deadline) {
			t.Fatalf("the test daemon never answered: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// serveTestPlugins runs impls as serve runs the bundled plugins of their ids, and waits until each is loaded.
func (e *Env) serveTestPlugins(t testing.TB, ctx context.Context, db *sqlite.DB, cfg config.Config, impls map[string]src.Source,
	logf func(string, ...any),
) (stop func()) {
	t.Helper()
	if len(impls) == 0 {
		return func() {}
	}
	manifests, err := bundled.Manifests()
	if err != nil {
		t.Fatal(err)
	}
	l := plugintest.NewLauncher(t)
	sums := host.Checksums{}
	for _, m := range manifests {
		impl, ok := impls[m.ID]
		if !ok {
			continue
		}
		e.plugins = append(e.plugins, host.Plugin{Manifest: m, Dir: t.TempDir(), Binary: brand.PluginBinary(m.ID), Bundled: true})
		sum := sha256.Sum256([]byte(m.ID))
		sums[m.ID] = sum[:]
		l.Add(m.ID, map[plugin.Kind]plugin.Impl{src.Kind: src.New(impl)})
	}
	if err := writeChecksums(host.ChecksumsFile(e.StateDir), sums); err != nil {
		t.Fatal(err)
	}
	e.pluginLauncher = l
	e.noJobs = false
	stop = e.servePlugins(ctx, db, cfg, logf)
	e.noJobs = true
	deadline := time.Now().Add(time.Minute)
	for id := range impls {
		for {
			if _, err := e.pluginKind.Client(id); err == nil {
				break
			} else if time.Now().After(deadline) {
				stop()
				t.Fatalf("plugin %s never loaded: %v", id, err)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	return stop
}

// Client is the daemon's client, as a person's clients speak to it.
func (d *TestDaemon) Client() *client.Client { return d.client }

// Start is a turn of intent on u that a new agent session started by hand, as a session asking for a round does.
func (d *TestDaemon) Start(t testing.TB, u unit.Unit, intent string) TestSession {
	t.Helper()
	n := d.handle.Add(1)
	s := TestSession{Handle: fmt.Sprintf("test-session-%d", n), Round: fmt.Sprintf("test-turn-%d", n), Unit: u}
	id, err := d.e.Turns.Resolve(d.ctx, testManager, s.Handle)
	if err != nil {
		t.Fatalf("taking session %s: %v", s.Handle, err)
	}
	if err := d.e.Turns.SessionStarted(d.ctx, turns.SessionStart{Session: id, Agent: "echo", Source: "startup"}); err != nil {
		t.Fatalf("starting session %s: %v", s.Handle, err)
	}
	if err := d.e.Turns.MCPSeen(d.ctx, s.Handle); err != nil {
		t.Fatalf("session %s: %v", s.Handle, err)
	}
	r := turns.SendRequest{Unit: u, Intent: dturn.Intent(intent), Turn: s.Round, Trigger: dturn.ByHand, Records: true, Agent: "echo"}
	if _, err := d.e.Turns.StartByHand(d.ctx, r, id); err != nil {
		t.Fatalf("starting a %s turn on %s: %v", intent, u.Label(), err)
	}
	return s
}

// EndTurn records the end of s's turn, as the agent's turn_end call does.
func (d *TestDaemon) EndTurn(t testing.TB, s TestSession, req api.TurnEndRequest) api.TurnEnded {
	t.Helper()
	got, err := d.client.EndTurn(d.ctx, s.Handle, req)
	if err != nil {
		t.Fatalf("ending turn %s: %v", s.Round, err)
	}
	return got
}

// Send sends the approved drafts once, as the daemon's sender does.
func (d *TestDaemon) Send(ctx context.Context) error {
	_, _, err := d.e.outbox.Send(ctx)
	return err
}

// Sweep runs the workflows once, as the daemon's sweep does.
func (d *TestDaemon) Sweep(ctx context.Context) error {
	if d.e.engine == nil {
		return errors.New("serve built no workflow engine")
	}
	_, _, err := d.e.engine.Sweep(ctx, d.loopMu)
	return err
}

// Book is u's turn book.
func (d *TestDaemon) Book(t testing.TB, u unit.Unit) dturn.Book {
	t.Helper()
	books, err := d.e.Turns.Store.Books(d.ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	return books[u]
}

var errNoShell = errors.New("no shell in a test daemon")

// servedNoGit runs no git: a test daemon has no checkout.
type servedNoGit struct{}

func (servedNoGit) Run(_ context.Context, dir string, _ []string, args ...string) ([]byte, error) {
	return nil, &gitx.Error{Args: args, Code: 128, Msg: "no git in a test daemon: " + dir}
}

type servedLog struct{ t testing.TB }

func (w servedLog) Write(p []byte) (int, error) {
	w.t.Log(string(p))
	return len(p), nil
}

// testManager is the workspace manager of a test daemon, which gives its sessions their handles.
const testManager = "fake"
