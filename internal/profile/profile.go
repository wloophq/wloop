// Package profile composes the reviewer's guide from blocks: the loop's
// bundled ones, then the user's profile, each addressed by a path of the
// conditions under which it applies.
package profile

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/profiles"
)

// Kind is an intent with judgment blocks, the profile a manifest names.
type Kind string

type Layer struct {
	// Name is bundled, user, or plugin:<id> and user:<id> for a plugin's
	// layer and the user's over it.
	Name string
	// Source is "embedded" for the bundled layer, the path in the binary
	// for a layer FS holds, else Dir.
	Source string
	// Dir is the directory of a layer on disk, "" for a layer the binary
	// holds.
	Dir string
	// FS holds a plugin's layer the binary bundles; nil for the bundled
	// layer and a layer on disk.
	FS fs.FS
}

// Bundled says whether l is the bundled layer, which every reader takes
// from the embedded default profile.
func (l Layer) Bundled() bool { return l.Dir == "" && l.FS == nil }

// Plugin says whether l is a plugin's own layer, not the user's over it.
func (l Layer) Plugin() bool { return strings.HasPrefix(l.Name, pluginLayer) }

// Embedded says whether the binary holds l: the bundled layer or a bundled
// plugin's.
func (l Layer) Embedded() bool { return l.Dir == "" }

type Part struct {
	Layer Layer
	File  string
	// Path is the block's path relative to its layer.
	Path     string
	Label    string
	Level    int
	Override bool
	Text     string
}

type Guide struct {
	Kind  Kind
	Parts []Part
	// Delegates come from the last part that declares them.
	Delegates     map[string][]string
	DelegatesFrom string
	// Skipped lists, as "<layer>: <file>", the blocks that need a brief the
	// pull request does not have.
	Skipped []string
	// Dropped lists the blocks left out to keep the judgment within JudgmentMax.
	Dropped []string
}

type MissingError struct{ Layer, Dir string }

func (e MissingError) Error() string {
	return fmt.Sprintf("%s profile %s not found", e.Layer, e.Dir)
}

// Chain is the bundled layer, the profile layer of each enabled plugin,
// the user's profile when one is set, then the user's plugins/<id>/ of each
// enabled plugin. A plugin's layer is its directory's profile/, else the
// one the binary bundles for it. A disabled plugin's layers are left out.
func Chain(cfg config.Config) []Layer {
	out := []Layer{{Name: "bundled", Source: "embedded"}}
	var found []PluginDir
	if ps := plugins.Load(); ps != nil {
		found = *ps
	}
	var bundled []PluginFS
	if bs := bundledPlugins.Load(); bs != nil {
		bundled = *bs
	}
	var on []string
	layers := map[string]Layer{}
	for _, p := range found {
		if !cfg.PluginEnabled(p.ID) || slices.Contains(on, p.ID) {
			continue
		}
		on = append(on, p.ID)
		if d := filepath.Join(p.Dir, PluginProfileDir); isDir(d) {
			layers[p.ID] = Layer{Name: PluginLayer(p.ID), Source: d, Dir: d}
		}
	}
	for _, b := range bundled {
		if !cfg.PluginEnabled(b.ID) {
			continue
		}
		if !slices.Contains(on, b.ID) {
			on = append(on, b.ID)
		}
		if _, ok := layers[b.ID]; !ok {
			layers[b.ID] = Layer{Name: PluginLayer(b.ID), Source: b.Source, FS: b.FS}
		}
	}
	for _, id := range on {
		if l, ok := layers[id]; ok {
			out = append(out, l)
		}
	}
	if cfg.Profile == "" {
		return out
	}
	out = append(out, Layer{Name: "user", Source: cfg.Profile, Dir: cfg.Profile})
	for _, id := range on {
		if d := filepath.Join(cfg.Profile, PluginsDir, id); isDir(d) {
			out = append(out, Layer{Name: "user:" + id, Source: d, Dir: d})
		}
	}
	return out
}

// BundledChain is the profile the release ships: the bundled layer and the
// layer of every plugin the binary bundles.
func BundledChain() []Layer {
	out := []Layer{{Name: "bundled", Source: "embedded"}}
	if bs := bundledPlugins.Load(); bs != nil {
		for _, b := range *bs {
			out = append(out, Layer{Name: PluginLayer(b.ID), Source: b.Source, FS: b.FS})
		}
	}
	return out
}

// PluginDir is a plugin's directory, which holds its profile layer.
type PluginDir struct {
	ID, Dir string
}

// PluginProfileDir is a plugin directory's profile layer, and PluginsDir
// the directory of a user profile that holds the user's layer of each
// plugin, by id.
const (
	PluginProfileDir = "profile"
	PluginsDir       = "plugins"
)

// PluginFS is the profile layer the binary bundles for a plugin; Source is
// its path in the binary.
type PluginFS struct {
	ID, Source string
	FS         fs.FS
}

const pluginLayer = "plugin:"

// PluginLayer is the name of plugin id's own layer.
func PluginLayer(id string) string { return pluginLayer + id }

var (
	plugins        atomic.Pointer[[]PluginDir]
	bundledPlugins atomic.Pointer[[]PluginFS]
)

// UsePlugins makes ps the plugins whose layers Chain adds. The composition
// root sets it once, after it discovers the plugins and before the profile
// loads.
func UsePlugins(ps []PluginDir) { plugins.Store(&ps) }

// UseBundledPlugins makes ps the layers the binary bundles, which Chain adds
// for an enabled plugin whose directory holds no layer or that no directory
// on disk holds. It is set once, before the profile loads.
func UseBundledPlugins(ps []PluginFS) { bundledPlugins.Store(&ps) }

// FSys is the files of l, relative to the layer.
func (l Layer) FSys() (fs.FS, error) {
	switch {
	case l.FS != nil:
		return l.FS, nil
	case l.Dir != "":
		return os.DirFS(l.Dir), nil
	}
	return fs.Sub(profiles.Default, "default")
}

// FileName is where rel, a slash path, is in l, as a problem names it: a
// path on disk for a layer on disk, else its path in the binary.
func (l Layer) FileName(rel string) string {
	switch {
	case l.Dir != "":
		return filepath.Join(l.Dir, filepath.FromSlash(rel))
	case l.FS != nil:
		return path.Join(l.Source, rel)
	}
	return path.Join("default", rel)
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// Sources lists one "<layer>: <file>" per part, in the text's order.
func (g Guide) Sources() []string {
	out := make([]string, 0, len(g.Parts))
	for _, p := range g.Parts {
		out = append(out, p.Layer.Name+": "+p.File)
	}
	return out
}

// Guides composes the guide of a pull request from a profile tree read once
// for the registry in force.
type Guides struct {
	Tree func() (Tree, error)
}

func (g Guides) Guide(kind Kind, f Facts) (Guide, error) {
	t, err := g.Tree()
	if err != nil {
		return Guide{Kind: kind}, err
	}
	return t.Compose(kind, f), nil
}

// Check reports the error of reading the tree, so a broken block stops a
// round before anything is touched for it.
func (g Guides) Check() error {
	_, err := g.Tree()
	return err
}

// BundledSHA hashes the bundled guide of kind, to tell when a new version
// changed a guide a profile replaces.
func BundledSHA(kind Kind) string {
	raw, err := fs.ReadFile(profiles.Default, "default/intent="+string(kind)+".md")
	if err != nil {
		return ""
	}
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])[:12]
}
