// Package bundled is the plugins the release bundles: their manifests, their
// profile layers, and the vocabulary they merge into the builtin tables,
// which the package installs, with the layers, when it initializes. The composition root imports it and installs
// the bundled and the discovered plugins' vocabulary over it; every test that
// loads the bundled profile, whose intents and workflows name the bundled
// plugins' facts, imports it too.
package bundled

import (
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"

	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/plugins/codec"
	"github.com/wloophq/wloop/internal/profile"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
	"github.com/wloophq/wloop/plugins"
)

// Manifests are the bundled plugins' manifests, by id order.
func Manifests() ([]*manifest.Manifest, error) {
	dirs, err := fs.ReadDir(plugins.Manifests, ".")
	if err != nil {
		return nil, err
	}
	var out []*manifest.Manifest
	for _, d := range dirs {
		raw, err := fs.ReadFile(plugins.Manifests, path.Join(d.Name(), manifest.File))
		if err != nil {
			return nil, err
		}
		m, err := manifest.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("bundled plugin %s: %w", d.Name(), err)
		}
		if m.ID != d.Name() {
			return nil, fmt.Errorf("bundled plugin %s: its manifest's id is %s", d.Name(), m.ID)
		}
		out = append(out, m)
	}
	return out, nil
}

// Profiles are the bundled plugins' profile layers, by id order.
func Profiles() ([]profile.PluginFS, error) {
	dirs, err := fs.ReadDir(plugins.Profiles, ".")
	if err != nil {
		return nil, err
	}
	var out []profile.PluginFS
	for _, d := range dirs {
		root := path.Join(d.Name(), profile.PluginProfileDir)
		if _, err := fs.Stat(plugins.Profiles, root); err != nil {
			continue
		}
		sub, err := fs.Sub(plugins.Profiles, root)
		if err != nil {
			return nil, err
		}
		out = append(out, profile.PluginFS{ID: d.Name(), Source: path.Join("plugins", root), FS: sub})
	}
	return out, nil
}

// Sources are the [source] tables of the bundled source plugins.
func Sources() ([]*manifest.Source, error) {
	ms, err := Manifests()
	if err != nil {
		return nil, err
	}
	var out []*manifest.Source
	for _, m := range ms {
		s, err := m.Source()
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// Vocabulary is the core rows and the bundled source plugins' vocabularies.
func Vocabulary() (*builtin.Vocabulary, error) {
	ss, err := Sources()
	if err != nil {
		return nil, err
	}
	return builtin.Merge(ss...)
}

// Codecs are the bundled source plugins' unit codecs, by id.
func Codecs() (map[unit.ProviderID]unit.Codec, error) {
	ss, err := Sources()
	if err != nil {
		return nil, err
	}
	out := map[unit.ProviderID]unit.Codec{}
	for _, s := range ss {
		c, err := codec.Of(s.Provider, s.Codec)
		if err != nil {
			return nil, err
		}
		out[unit.ProviderID(s.Provider)] = c
	}
	return out, nil
}

func init() {
	v, err := Vocabulary()
	if err != nil {
		panic(err)
	}
	builtin.Install(v)
	cs, err := Codecs()
	if err != nil {
		panic(err)
	}
	unit.UseLabels(unit.NewRegistry(slices.Collect(maps.Values(cs))...))
	ps, err := Profiles()
	if err != nil {
		panic(err)
	}
	profile.UseBundledPlugins(ps)
}
