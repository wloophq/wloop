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
	"path/filepath"
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
	// Source is "embedded" for the bundled layer.
	Source string
	// Dir is "" for the bundled layer.
	Dir string
}

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
// enabled plugin. A disabled plugin's layers are left out.
func Chain(cfg config.Config) []Layer {
	out := []Layer{{Name: "bundled", Source: "embedded"}}
	ps := plugins.Load()
	if ps == nil {
		ps = &[]PluginDir{}
	}
	var on []PluginDir
	for _, p := range *ps {
		if cfg.PluginEnabled(p.ID) {
			on = append(on, p)
		}
	}
	for _, p := range on {
		if d := filepath.Join(p.Dir, PluginProfileDir); isDir(d) {
			out = append(out, Layer{Name: "plugin:" + p.ID, Source: d, Dir: d})
		}
	}
	if cfg.Profile == "" {
		return out
	}
	out = append(out, Layer{Name: "user", Source: cfg.Profile, Dir: cfg.Profile})
	for _, p := range on {
		if d := filepath.Join(cfg.Profile, PluginsDir, p.ID); isDir(d) {
			out = append(out, Layer{Name: "user:" + p.ID, Source: d, Dir: d})
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

var plugins atomic.Pointer[[]PluginDir]

// UsePlugins makes ps the plugins whose layers Chain adds. The composition
// root sets it once, after it discovers the plugins and before the profile
// loads.
func UsePlugins(ps []PluginDir) { plugins.Store(&ps) }

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
