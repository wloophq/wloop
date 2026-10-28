package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/task"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/source"
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

// withoutGitHub is d with nothing of GitHub's when its plugin is disabled:
// no brief queue, intakes, explorer, budget or viewer read.
func withoutGitHub(d daemon.Deps) daemon.Deps {
	if d.Cfg.PluginEnabled(string(unit.GitHub)) {
		return d
	}
	d.Brief, d.BriefStatus, d.BriefViews, d.BriefsUnavailable = nil, nil, nil, ""
	d.Intakes, d.IntakeBriefs, d.Explorer, d.Budget, d.Viewer = nil, nil, nil, nil, nil
	return d
}
