package cli

import (
	"io/fs"
	"os"
	"path/filepath"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/unit"
	intentload "github.com/wloophq/wloop/internal/intent"
	psource "github.com/wloophq/wloop/internal/plugins/source"
)

// uiOf is what a plugin's clients show of it, from its manifest and its
// profile layer, and the directory of its web assets the host serves
// under /plugins/<id>/; nil, nil for a plugin with neither.
type uiOf func(id unit.ProviderID) (*api.PluginUI, fs.FS)

// pluginCatalog is plugin id's catalog, worded by the profile as it last
// loaded; one with no manifest has none.
func (e *Env) pluginCatalog(id unit.ProviderID) psource.Catalog {
	s, err := e.pluginSource(id)
	if err != nil {
		return psource.Catalog{}
	}
	return psource.Catalog{Source: s, Texts: func() intentload.UITexts {
		l := e.loadedIntents()
		if l == nil {
			return intentload.UITexts{}
		}
		return l.UI[string(id)]
	}}
}

// pluginUI is plugin id's UI and the web directory of its plugin
// directory on disk that its manifest names.
func (e *Env) pluginUI(id unit.ProviderID) (*api.PluginUI, fs.FS) {
	c := e.pluginCatalog(id)
	ui := c.PluginUI()
	if ui == nil || c.Source.UI.Web == "" {
		return ui, nil
	}
	for _, p := range e.plugins {
		if p.ID() == string(id) {
			dir := filepath.Join(p.Dir, filepath.FromSlash(c.Source.UI.Web))
			if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
				return ui, os.DirFS(dir)
			}
		}
	}
	return ui, nil
}
