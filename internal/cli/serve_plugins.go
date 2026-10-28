package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/hashicorp/go-hclog"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/plugins/codec"
	psource "github.com/wloophq/wloop/internal/plugins/source"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/snapshot"
	"github.com/wloophq/wloop/internal/domain/task"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/plugins/bundled"
	"github.com/wloophq/wloop/internal/plugins/host"
	"github.com/wloophq/wloop/internal/profile"
	"github.com/wloophq/wloop/internal/source"
	"github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// buildSources builds the module of every enabled plugin; one that fails
// to build is left out and named in the error.
func buildSources(ps []source.Plugin, cfg config.Config, sd source.Deps) ([]source.Module, error) {
	var mods []source.Module
	var errs []error
	for _, p := range ps {
		if !cfg.PluginEnabled(string(p.ID)) {
			continue
		}
		m, err := p.Build(cfg, sd)
		if err == nil {
			err = m.Valid()
		}
		if err == nil {
			err = catalog.CheckPluginKeys(m.ID, m.UI)
		}
		if err == nil && m.ID != p.ID {
			err = fmt.Errorf("source %s: the plugin built a module of %s", p.ID, m.ID)
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		mods = append(mods, m)
	}
	return mods, errors.Join(errs...)
}

// withSources is d with the plugins registered: every plugin's codec, with
// the session's and the workspace's, parses the unit routes, a disabled
// one's included; the built modules' ingestion jobs, slow jobs, rows,
// streams, jobs and health run in the daemon; Track reads a unit through
// its source.
func withSources(d daemon.Deps, ps []source.Plugin, mods []source.Module) daemon.Deps {
	codecs := []unit.Codec{dturn.SessionCodec{}, wsdomain.Codec{}, task.Codec{}}
	for _, p := range ps {
		codecs = append(codecs, p.Codec)
	}
	tracks := map[unit.ProviderID]func(context.Context, unit.Unit) (port.TrackRead, error){}
	untracks := map[unit.ProviderID]func(context.Context, unit.Unit) error{}
	var slow slowJobs
	var resolvers []func(context.Context, string) (unit.Unit, bool, error)
	for _, m := range mods {
		if m.Resolve != nil {
			resolvers = append(resolvers, m.Resolve)
		}
		d.Ingest = append(d.Ingest, daemon.IngestJob{ID: m.ID, Job: m.Ingest})
		d.RowProviders = append(d.RowProviders, m.Rows)
		if m.Track != nil {
			tracks[m.ID] = m.Track
		}
		if m.Track != nil && m.Untrack != nil {
			untracks[m.ID] = m.Untrack
		}
		if m.Slow != nil {
			slow = append(slow, m.Slow)
		}
		d.Runtimes = append(d.Runtimes, runtimeOf(m))
	}
	d.Units = unit.NewRegistry(codecs...)
	if len(tracks) > 0 {
		d.Track = func(ctx context.Context, u unit.Unit) (port.TrackRead, error) {
			track, ok := tracks[u.Provider]
			if !ok {
				return port.TrackRead{}, fmt.Errorf("%w: no source tracks %s", port.ErrNotFound, u.Label())
			}
			return track(ctx, u)
		}
	}
	if len(untracks) > 0 {
		d.Follows = func(p unit.ProviderID) bool { return untracks[p] != nil }
		d.Untrack = func(ctx context.Context, u unit.Unit) error {
			untrack, ok := untracks[u.Provider]
			if !ok {
				return fmt.Errorf("%w: no source untracks %s", port.ErrNotFound, u.Label())
			}
			return untrack(ctx, u)
		}
	}
	if len(slow) > 0 {
		d.Collector = slow
	}
	if len(resolvers) > 0 {
		d.Resolve = func(ctx context.Context, address string) (unit.Unit, bool, error) {
			for _, resolve := range resolvers {
				if u, ok, err := resolve(ctx, address); err != nil || ok {
					return u, ok, err
				}
			}
			return unit.Unit{}, false, nil
		}
	}
	d.Plugins = pluginList(ps, mods, d.Cfg)
	d.PluginEnabled = pluginSwitch(ps, mods, d.Cfg)
	return d
}

func runtimeOf(m source.Module) daemon.PluginRuntime {
	rt := daemon.PluginRuntime{ID: m.ID, Run: m.Run, Health: m.Health, WebAssets: m.WebAssets}
	for _, j := range m.Jobs {
		rt.Jobs = append(rt.Jobs, daemon.PluginJob{Name: j.Name, Every: j.Every, Timeout: j.Timeout, Run: j.Run})
	}
	return rt
}

// withoutPluginJobs are rts with no stream and no job: their health and
// assets only.
func withoutPluginJobs(rts []daemon.PluginRuntime) []daemon.PluginRuntime {
	out := make([]daemon.PluginRuntime, len(rts))
	for i, rt := range rts {
		out[i] = daemon.PluginRuntime{ID: rt.ID, Health: rt.Health, WebAssets: rt.WebAssets}
	}
	return out
}

// pluginSwitch is the daemon's PluginEnabled: nil while every compiled-in
// plugin is enabled. A plugin is on when its config enables it and its
// module was built.
func pluginSwitch(ps []source.Plugin, mods []source.Module, cfg config.Config) func(unit.ProviderID) bool {
	off := map[unit.ProviderID]bool{}
	for _, p := range ps {
		if !cfg.PluginEnabled(string(p.ID)) || !slices.ContainsFunc(mods, func(m source.Module) bool { return m.ID == p.ID }) {
			off[p.ID] = true
		}
	}
	if len(off) == 0 {
		return nil
	}
	return func(p unit.ProviderID) bool { return !off[p] }
}

// pluginList is every compiled-in plugin as GET /api/plugins serves it: a
// disabled one, or one refused at build, with no UI.
func pluginList(ps []source.Plugin, mods []source.Module, cfg config.Config) []api.Plugin {
	built := map[unit.ProviderID]source.Module{}
	for _, m := range mods {
		built[m.ID] = m
	}
	out := make([]api.Plugin, 0, len(ps))
	for _, p := range ps {
		ap := api.Plugin{ID: p.ID, Name: p.Name}
		if m, ok := built[p.ID]; ok && cfg.PluginEnabled(string(p.ID)) {
			ap.Enabled, ap.UI, ap.Reacts, ap.Capabilities = true, m.UI, m.Reacts, m.Capabilities()
		}
		out = append(out, ap)
	}
	return out
}

// disabledPlugins names the plugins the daemon serves disabled.
func disabledPlugins(ps []api.Plugin) []string {
	var out []string
	for _, p := range ps {
		if !p.Enabled {
			out = append(out, string(p.ID))
		}
	}
	return out
}

// installed are the plugins InstallPlugins found, which NewEnv's Env runs.
var installed []host.Plugin

// InstallPlugins finds and installs the plugins, as installPlugins does,
// for the process's Envs. The command's main calls it once, before NewEnv
// and before anything reads the configuration or the profile.
func InstallPlugins() error {
	exe, err := os.Executable()
	if err != nil {
		exe = ""
	}
	installed, err = installPlugins(config.Dir(), exe)
	return err
}

// installPlugins finds the plugins in configDir's plugins directory and in
// the bundled one beside exe, and installs what the configuration and the
// profile read of every plugin, enabled or not: the bundled and found
// plugins' vocabulary, their codecs' labels, and their profile layers,
// which the profile loads while a plugin is enabled. A plugin whose
// manifest or vocabulary does not fit is left out and named in the error.
func installPlugins(configDir, exe string) ([]host.Plugin, error) {
	found, err := host.Discover(host.Roots(configDir, exe)...)
	errs := []error{err}
	srcs, err := bundled.Sources()
	if err != nil {
		return nil, err
	}
	var dirs []profile.PluginDir
	var kept []host.Plugin
	for _, p := range found {
		s, err := p.Manifest.Source()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		srcs = slices.DeleteFunc(srcs, func(b *manifest.Source) bool { return b.Provider == s.Provider })
		srcs = append(srcs, s)
		kept = append(kept, p)
		dirs = append(dirs, profile.PluginDir{ID: p.ID(), Dir: p.Dir})
	}
	v, err := builtin.Merge(srcs...)
	errs = append(errs, err)
	builtin.Install(v)
	codecs := []unit.Codec{dturn.SessionCodec{}, wsdomain.Codec{}, task.Codec{}}
	for _, s := range srcs {
		c, err := codec.Of(s.Provider, s.Codec)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		codecs = append(codecs, c)
	}
	unit.UseLabels(unit.NewRegistry(codecs...))
	profile.UsePlugins(dirs)
	return kept, errors.Join(errs...)
}

// servePlugins runs the plugins of e that cfg enables until ctx ends or
// the returned stop runs, which waits for them to stop; under --no-jobs it
// runs none.
func (e *Env) servePlugins(ctx context.Context, store snapshot.Store, cfg config.Config, logf func(string, ...any)) (stop func()) {
	snaps := psource.NewSnapshots(store)
	codecs := e.unitCodecs()
	note := func(t port.Topic, units ...unit.Unit) {
		if e.changes != nil {
			e.changes.Note(t, units...)
		}
	}
	snaps.OnStale(func(unit.ProviderID) {
		if e.changes != nil {
			e.changes.NoteAll(port.TopicPRs)
		}
	})
	e.pluginKind = &psource.Kind{
		Snapshots: snaps,
		StateDir:  func(id string) string { return filepath.Join(e.StateDir, "plugins", id) },
		Broker: func(id string, s *manifest.Source) *psource.Broker {
			return &psource.Broker{ID: id, Manifest: s, Snapshots: snaps, Units: psource.Units{Codecs: codecs}, Config: e.currentConfig,
				Note: note}
		},
		Loaded: func(id string) {
			logf("plugin %s: loaded", id)
			note(port.TopicPRs | port.TopicJobs)
		},
		Logf: logf,
	}
	if e.noJobs {
		logf("plugins: --no-jobs starts none")
		return func() {}
	}
	sums, err := host.ReadChecksums(filepath.Join(e.StateDir, "plugins", "checksums"))
	if err != nil {
		logf("plugins: %v", err)
	}
	runs, why, err := host.Runs(e.plugins, cfg, sums, []string{plugin.BrandEnv + "=" + brand.Name}, pluginLogger(logf))
	if err != nil {
		logf("%v", err)
	}
	for _, r := range runs {
		logf("plugin %s: runs %s, %s", r.Manifest.ID, r.Spec.Path, why[r.Manifest.ID])
		e.pluginKind.Expect(r.Manifest.ID)
	}
	var launcher plugin.Launcher = host.ExecLauncher{}
	if e.pluginLauncher != nil {
		launcher = e.pluginLauncher
	}
	h := &host.Host{Launcher: launcher, Kinds: []host.Kind{e.pluginKind}, Logf: logf}
	pctx, cancel := context.WithCancel(ctx)
	var notifying sync.WaitGroup
	if e.changes != nil {
		from := e.changes.Gen()
		notifying.Go(func() { e.pluginKind.Notify(pctx, e.changes, from) })
	}
	h.Start(pctx, runs)
	return func() {
		cancel()
		notifying.Wait()
		h.Wait()
	}
}

// moduleHost is the broker the host serves a built-in source module of
// provider id: its events, marks and drafts are the host's, in db.
func (e *Env) moduleHost(db *sqlite.DB) func(id unit.ProviderID) src.Host {
	codecs := e.unitCodecs()
	return func(id unit.ProviderID) src.Host {
		b := &psource.Broker{ID: string(id), Units: psource.Units{Codecs: codecs}, Config: e.currentConfig,
			Events: sqlite.New(db), Marks: db, Drafts: db}
		return b.Host()
	}
}

// unitCodecs are the codecs of every provider: the core's and every
// plugin's, enabled or not.
func (e *Env) unitCodecs() *unit.Registry {
	codecs := []unit.Codec{dturn.SessionCodec{}, wsdomain.Codec{}, task.Codec{}}
	for _, p := range e.sourcePlugins() {
		codecs = append(codecs, p.Codec)
	}
	for _, p := range e.plugins {
		if s, err := p.Manifest.Source(); err == nil {
			if c, err := codec.Of(s.Provider, s.Codec); err == nil {
				codecs = append(codecs, c)
			}
		}
	}
	return unit.NewRegistry(codecs...)
}

// pluginLogger relays a plugin's log lines to the daemon's log.
func pluginLogger(logf func(string, ...any)) hclog.Logger {
	return hclog.New(&hclog.LoggerOptions{Output: logWriter(logf), Level: hclog.Info, JSONFormat: false})
}

// logWriter writes each line to logf.
type logWriter func(string, ...any)

func (w logWriter) Write(b []byte) (int, error) {
	w("%s", strings.TrimRight(string(b), "\n"))
	return len(b), nil
}
