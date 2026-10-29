package cli

import (
	"context"
	"slices"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/domain/unit"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/internal/readmodel"
	rowoverlay "github.com/wloophq/wloop/internal/readmodel/overlay"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
)

// reportedPlugin is a plugin the host runs, whose units are its reports.
type reportedPlugin struct {
	id  unit.ProviderID
	src *manifest.Source
}

// reportedPlugins are the plugins of e that cfg enables and the host runs,
// none of them served by a built module; none outside serve.
func (e *Env) reportedPlugins(cfg config.Config, mods []pluginModule) []reportedPlugin {
	if e.pluginKind == nil {
		return nil
	}
	var out []reportedPlugin
	for _, p := range e.plugins {
		s, err := p.Manifest.Source()
		if err != nil || p.Binary == "" || !cfg.PluginEnabled(p.ID()) {
			continue
		}
		id := unit.ProviderID(p.ID())
		if slices.ContainsFunc(mods, func(m pluginModule) bool { return m.ID == id }) {
			continue
		}
		out = append(out, reportedPlugin{id: id, src: s})
	}
	return out
}

// reportedSource is what plugin p's rows and cards read: its reports in
// the store, under the mutes the overrides hold.
func (e *Env) reportedSource(p reportedPlugin, store *inventory.Store, rd readmodel.RowDeps) readmodel.ReportedSource {
	snaps := e.pluginKind.Snapshots
	return readmodel.ReportedSource{RowDeps: rd, ID: p.id, Reports: snaps, Mutes: storeMutes{store}, Stale: snaps.Stale,
		Catalog: e.pluginCatalog(p.id)}
}

// wireReported serves the units of every plugin the host runs: rows and
// workflow facts from their last reports, every plugin's unit routes.
func (e *Env) wireReported(w *serveWiring) {
	ps := e.reportedPlugins(w.Cfg, w.Modules)
	if len(ps) == 0 || w.DB == nil {
		return
	}
	store := inventory.New(w.DB)
	if w.Engine.Subjects == nil {
		w.Engine.Subjects = map[unit.ProviderID]automation.Units{}
	}
	for _, p := range ps {
		w.Deps.RowProviders = append(w.Deps.RowProviders, func(rd readmodel.RowDeps) readmodel.Provider {
			return readmodel.NewReported(e.reportedSource(p, store, rd))
		})
		w.Engine.Subjects[p.id] = psource.UnitFacts{Snapshots: e.pluginKind.Snapshots}
	}
	w.Deps.Units = e.unitCodecs()
}

// storeMutes are the mutes the overrides hold, as the rows overlay them.
type storeMutes struct{ store *inventory.Store }

func (m storeMutes) Mutes(ctx context.Context, units []unit.Unit) (map[unit.Unit]rowoverlay.Mute, error) {
	got, err := m.store.UnitMutes(ctx, units)
	if err != nil {
		return nil, err
	}
	out := make(map[unit.Unit]rowoverlay.Mute, len(got))
	for u, x := range got {
		out[u] = rowoverlay.Mute{Until: x.Until, UntilStateChange: x.UntilStateChange, Kind: x.Kind, WaitingOn: x.WaitingOn}
	}
	return out, nil
}
