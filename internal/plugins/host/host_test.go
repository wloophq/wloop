package host_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/hashicorp/go-hclog"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/plugins/codec"
	"github.com/wloophq/wloop/internal/plugins/host"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
	"github.com/wloophq/wloop/pkg/plugin/plugintest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/pkg/plugin/source/sourcetest"
)

const childEnv = "HOST_TEST_CHILD"

func TestMain(m *testing.M) {
	if os.Getenv(childEnv) == "source" {
		plugin.Serve(map[plugin.Kind]plugin.Impl{src.Kind: src.New(sourcetest.NewStub())})
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func manifestOf(id string) string {
	return "id = \"" + id + "\"\nkinds = [\"source\"]\nprotocol = 1\n[source]\nname = \"Acme\"\nslot = \"" + id + "\"\ncodec = {keys = [\"{name}\"]}\n"
}

// installPlugin writes plugin id's directory under root, with a binary that
// is a copy of bin when bin is set.
func installPlugin(t *testing.T, root, id, bin string) {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, manifest.File), []byte(manifestOf(id)), 0o644); err != nil {
		t.Fatal(err)
	}
	raw := []byte("#!/bin/sh\n")
	if bin != "" {
		var err error
		if raw, err = os.ReadFile(bin); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, brand.PluginBinary(id)), raw, 0o755); err != nil {
		t.Fatal(err)
	}
}

// settings switch and trust plugins as a config.toml would.
type settings struct {
	off   []string
	trust map[string]string
}

func (s settings) PluginEnabled(id string) bool { return !slices.Contains(s.off, id) }
func (s settings) PluginTrust(id string) string { return s.trust[id] }

// sourceKind is the source adapter over fresh snapshots.
func sourceKind(t *testing.T) *psource.Kind {
	snaps := psource.NewSnapshots()
	return &psource.Kind{
		Snapshots: snaps,
		StateDir:  func(string) string { return t.TempDir() },
		Broker: func(id string, s *manifest.Source) *psource.Broker {
			c, err := codec.Of(id, s.Codec)
			if err != nil {
				t.Fatal(err)
			}
			return &psource.Broker{ID: id, Manifest: s, Snapshots: snaps, Units: psource.Units{Codecs: unit.NewRegistry(c)},
				Config: func() config.Config { return config.Config{} }}
		},
	}
}

// eventually polls cond; its deadline only guards a hang.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("never: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func runHost(t *testing.T, h *host.Host, runs []host.Run) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	h.Start(ctx, runs)
	t.Cleanup(func() {
		cancel()
		h.Wait()
	})
	return cancel
}

func TestHostStartsAnEnabledPlugin(t *testing.T) {
	root := t.TempDir()
	installPlugin(t, root, "acme", "")
	installPlugin(t, root, "wiki", "")
	ps, err := host.Discover(host.Root{Dir: root})
	if err != nil || len(ps) != 2 {
		t.Fatalf("discover: %v %v", ps, err)
	}
	runs, _, err := host.Runs(ps, settings{off: []string{"wiki"}, trust: map[string]string{"acme": "path", "wiki": "path"}}, nil, nil, hclog.NewNullLogger())
	if err != nil || len(runs) != 1 || runs[0].Manifest.ID != "acme" {
		t.Fatalf("runs %v, %v", runs, err)
	}
	stub := sourcetest.NewStub(src.Snapshot{Unit: plugin.Unit{Provider: "acme", Key: "one"}, Rev: 1})
	l := plugintest.NewLauncher(t)
	l.Add("acme", map[plugin.Kind]plugin.Impl{src.Kind: src.New(stub)})
	l.Add("wiki", map[plugin.Kind]plugin.Impl{src.Kind: src.New(sourcetest.NewStub())})
	k := sourceKind(t)
	runHost(t, &host.Host{Launcher: l, Kinds: []host.Kind{k}}, runs)

	eventually(t, "acme started", func() bool { _, err := k.Client("acme"); return err == nil })
	c, _ := k.Client("acme")
	if _, err := c.Ingest(t.Context(), src.IngestRequest{Job: "ingest"}); err != nil {
		t.Fatal(err)
	}
	one := unit.Unit{Provider: "acme", Key: "one"}
	if _, ok := k.Snapshots.Last(one); !ok || k.Snapshots.Stale(one) {
		t.Errorf("acme's report reached the host: %t, stale %t", ok, k.Snapshots.Stale(one))
	}
	if l.Launches("acme") != 1 || l.Launches("wiki") != 0 {
		t.Errorf("launches: acme %d, wiki %d", l.Launches("acme"), l.Launches("wiki"))
	}
}

// fakeProcess is a plugin process that runs until crashed.
type fakeProcess struct {
	mu     sync.Mutex
	exited bool
}

func (p *fakeProcess) Kind(plugin.Kind) (*plugin.Client, error) { return new(plugin.Client), nil }
func (p *fakeProcess) Protocol() int                            { return plugin.Protocol }
func (p *fakeProcess) Kill()                                    { p.crash() }
func (p *fakeProcess) crash() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.exited = true
}

func (p *fakeProcess) Exited() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exited
}

// fakeLauncher launches fakeProcesses and records when.
type fakeLauncher struct {
	mu    sync.Mutex
	at    []time.Time
	procs []*fakeProcess
}

func (l *fakeLauncher) Launch(context.Context, plugin.Spec) (plugin.Process, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	p := &fakeProcess{}
	l.at, l.procs = append(l.at, time.Now()), append(l.procs, p)
	return p, nil
}

func (l *fakeLauncher) last() *fakeProcess {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.procs[len(l.procs)-1]
}

func (l *fakeLauncher) launches() []time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.at)
}

// fakeKind records what the host did with a kind.
type fakeKind struct {
	mu                  sync.Mutex
	starts, stops, down int
}

func (k *fakeKind) Kind() plugin.Kind { return src.Kind }
func (k *fakeKind) Start(context.Context, *manifest.Manifest, *plugin.Client) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.starts++
	return nil
}
func (k *fakeKind) Stop(context.Context, string) { k.mu.Lock(); k.stops++; k.mu.Unlock() }
func (k *fakeKind) Down(string)                  { k.mu.Lock(); k.down++; k.mu.Unlock() }

func runOf(t *testing.T, id string) host.Run {
	m, err := manifest.Parse([]byte(manifestOf(id)))
	if err != nil {
		t.Fatal(err)
	}
	return host.Run{Manifest: m, Spec: plugin.Spec{ID: id, Kinds: m.Kinds}}
}

// nextLaunch sleeps, in the bubble, until l launched more than n times.
func nextLaunch(l *fakeLauncher, n int) time.Time {
	for len(l.launches()) <= n {
		time.Sleep(time.Millisecond)
	}
	synctest.Wait()
	return l.launches()[n]
}

func TestACrashedPluginRestartsWithBackoffAndItsUnitsGoStale(t *testing.T) {
	t.Run("backoff", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l, k := &fakeLauncher{}, &fakeKind{}
			poll := 100 * time.Millisecond
			h := &host.Host{Launcher: l, Kinds: []host.Kind{k}, MinBackoff: time.Second, MaxBackoff: 4 * time.Second, Poll: poll}
			ctx, cancel := context.WithCancel(t.Context())
			h.Start(ctx, []host.Run{runOf(t, "acme")})
			launched := nextLaunch(l, 0)
			// A crash at launch is seen at the next poll, then waits its
			// backoff, doubling to the cap.
			for i, backoff := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 4 * time.Second} {
				l.last().crash()
				next := nextLaunch(l, i+1)
				if took := next.Sub(launched); took != poll+backoff {
					t.Errorf("restart %d after %s, want %s", i+1, took, poll+backoff)
				}
				launched = next
			}
			// A run that lasted past the cap restarts after the least backoff.
			crashed := launched.Add(4*time.Second + 50*time.Millisecond)
			time.Sleep(time.Until(crashed))
			l.last().crash()
			if took := nextLaunch(l, 5).Sub(crashed); took != 50*time.Millisecond+time.Second {
				t.Errorf("restart after a long run took %s", took)
			}
			cancel()
			h.Wait()
			if k.starts != 6 || k.down != 5 || k.stops != 1 {
				t.Errorf("starts %d, down %d, stops %d", k.starts, k.down, k.stops)
			}
		})
	})
	t.Run("stale units", func(t *testing.T) {
		stub := sourcetest.NewStub(src.Snapshot{Unit: plugin.Unit{Provider: "acme", Key: "one"}, Rev: 1})
		l := plugintest.NewLauncher(t)
		l.Add("acme", map[plugin.Kind]plugin.Impl{src.Kind: src.New(stub)})
		k := sourceKind(t)
		runHost(t, &host.Host{Launcher: l, Kinds: []host.Kind{k}, MinBackoff: 300 * time.Millisecond, Poll: time.Millisecond}, []host.Run{runOf(t, "acme")})
		one := unit.Unit{Provider: "acme", Key: "one"}
		ingest := func() bool {
			c, err := k.Client("acme")
			if err != nil {
				return false
			}
			_, err = c.Ingest(t.Context(), src.IngestRequest{Job: "ingest"})
			return err == nil
		}
		eventually(t, "a first report", ingest)
		l.Crash("acme")
		eventually(t, "the crashed plugin's units go stale", func() bool { return k.Snapshots.Stale(one) })
		if _, err := k.Client("acme"); !errors.Is(err, src.ErrUnavailable) {
			t.Errorf("a down plugin's client: %v", err)
		}
		eventually(t, "a restart", func() bool { return l.Launches("acme") == 2 && !k.Snapshots.Stale(one) })
		if snap, ok := k.Snapshots.Last(one); !ok || snap.Rev != 1 {
			t.Errorf("a stale unit lost its last snapshot: %v %t", snap, ok)
		}
		eventually(t, "a report after the restart", ingest)
	})
}

// linkPlugin installs plugin id under root with this test binary, which
// serves a source kind when childEnv says so, as its binary.
func linkPlugin(t *testing.T, root, id string) string {
	t.Helper()
	installPlugin(t, root, id, "")
	bin := filepath.Join(root, id, brand.PluginBinary(id))
	if err := os.Remove(bin); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(self, bin); err != nil {
		t.Fatal(err)
	}
	return bin
}

// pids records the processes an ExecLauncher started.
type pids struct {
	host.ExecLauncher
	mu  sync.Mutex
	got []int
}

func (p *pids) Launch(ctx context.Context, spec plugin.Spec) (plugin.Process, error) {
	proc, err := p.ExecLauncher.Launch(ctx, spec)
	if err == nil {
		p.mu.Lock()
		p.got = append(p.got, proc.(interface{ Pid() int }).Pid())
		p.mu.Unlock()
	}
	return proc, err
}

func (p *pids) all() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.got)
}

func alive(pid int) bool { return !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) }

func TestShutdownLeavesNoPluginProcess(t *testing.T) {
	root := t.TempDir()
	linkPlugin(t, root, "acme")
	ps, err := host.Discover(host.Root{Dir: root})
	if err != nil {
		t.Fatal(err)
	}
	runs, _, err := host.Runs(ps, settings{trust: map[string]string{"acme": host.TrustPath}}, nil, []string{childEnv + "=source"}, hclog.NewNullLogger())
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs %v, %v", runs, err)
	}
	l, k := &pids{}, sourceKind(t)
	h := &host.Host{Launcher: l, Kinds: []host.Kind{k}}
	ctx, cancel := context.WithCancel(context.Background())
	h.Start(ctx, runs)
	eventually(t, "the plugin runs", func() bool { _, err := k.Client("acme"); return err == nil })
	pid := l.all()[0]
	if !alive(pid) {
		t.Fatalf("plugin %d is not running", pid)
	}
	cancel()
	h.Wait()
	eventually(t, "no plugin process left", func() bool { return !alive(pid) })
	if _, err := k.Client("acme"); err == nil {
		t.Error("a stopped plugin keeps its client")
	}
}

func TestAnUntrustedBinaryIsRefused(t *testing.T) {
	user, bundled := t.TempDir(), t.TempDir()
	installPlugin(t, user, "acme", "")
	bin := linkPlugin(t, bundled, "beta")
	ps, err := host.Discover(host.Root{Dir: user}, host.Root{Dir: bundled, Bundled: true})
	if err != nil || len(ps) != 2 {
		t.Fatalf("discover %v, %v", ps, err)
	}
	env := []string{childEnv + "=source"}

	runs, why, err := host.Runs(ps, settings{}, nil, env, hclog.NewNullLogger())
	if !errors.Is(err, host.ErrUntrusted) || !strings.Contains(err.Error(), `[plugins.acme] trust = "path"`) {
		t.Errorf("an unrecorded binary outside the bundled directory: %v", err)
	}
	if len(runs) != 1 || runs[0].Manifest.ID != "beta" || runs[0].Spec.Checksum != nil || why["beta"] != "dev build, no checksum" {
		t.Errorf("a bundled plugin with no record: %v, %v", runs, why)
	}
	runs, _, err = host.Runs(ps, settings{trust: map[string]string{"acme": host.TrustPath}}, nil, env, hclog.NewNullLogger())
	if err != nil || len(runs) != 2 {
		t.Errorf("trust = path: %v, %v", runs, err)
	}

	raw, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	for _, c := range []struct {
		name string
		sum  []byte
		runs bool
	}{
		{"the recorded checksum", sum[:], true},
		{"another checksum", make([]byte, 32), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			runs, _, err := host.Runs(ps, settings{off: []string{"acme"}}, host.Checksums{"beta": c.sum}, env, hclog.NewNullLogger())
			if err != nil || len(runs) != 1 {
				t.Fatalf("runs %v, %v", runs, err)
			}
			p, err := host.ExecLauncher{}.Launch(t.Context(), runs[0].Spec)
			if p != nil {
				p.Kill()
			}
			if (err == nil) != c.runs {
				t.Errorf("launch: %v", err)
			}
		})
	}
}
