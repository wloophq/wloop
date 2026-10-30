package host

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
)

// Plugin is a plugin found on disk: its manifest, its directory and its
// binary.
type Plugin struct {
	Manifest *manifest.Manifest
	// Dir is the plugin's directory, which holds plugin.toml, the binary
	// and its profile layer.
	Dir string
	// Binary is the plugin's binary in Dir; "" when Dir holds none, as a
	// plugin compiled into the daemon has none.
	Binary string
	// Bundled says Dir is in the bundled directory beside the daemon's
	// binary, where a release installs its plugins.
	Bundled bool
}

// ID is the plugin's id.
func (p Plugin) ID() string { return p.Manifest.ID }

// Root is a directory of plugin directories, one per id.
type Root struct {
	Dir     string
	Bundled bool
}

// InstalledDir is where install places the bundled plugins of the release
// it runs from, under the state directory.
func InstalledDir(stateDir string) string { return filepath.Join(stateDir, "plugins", "bundled") }

// Roots are where plugins are found: the user's, <config>/plugins; the
// bundled ones install placed, InstalledDir; then the bundled directory
// beside the binary, <exe dir>/plugins, where a release and a development
// build keep them.
func Roots(configDir, stateDir, exe string) []Root {
	out := []Root{{Dir: filepath.Join(configDir, "plugins")}, {Dir: InstalledDir(stateDir), Bundled: true}}
	if exe != "" {
		out = append(out, Root{Dir: filepath.Join(filepath.Dir(exe), "plugins"), Bundled: true})
	}
	return out
}

// Discover reads every <root>/<id>/plugin.toml. A plugin found in more than
// one root is the first root's: a user's directory overrides a bundled
// plugin of the same id. A directory whose manifest does not load, or whose
// id is not its name, is left out and named in the error.
func Discover(roots ...Root) ([]Plugin, error) {
	var out []Plugin
	var errs []error
	for _, r := range roots {
		entries, err := os.ReadDir(r.Dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			dir := filepath.Join(r.Dir, e.Name())
			m, err := manifest.Load(dir)
			switch {
			case errors.Is(err, fs.ErrNotExist):
				continue
			case err != nil:
				errs = append(errs, err)
				continue
			case m.ID != e.Name():
				errs = append(errs, fmt.Errorf("%s: the manifest's id is %s", dir, m.ID))
				continue
			}
			if slices.ContainsFunc(out, func(p Plugin) bool { return p.ID() == m.ID }) {
				continue
			}
			p := Plugin{Manifest: m, Dir: dir, Bundled: r.Bundled}
			if bin := filepath.Join(dir, brand.PluginBinary(m.ID)); isFile(bin) {
				p.Binary = bin
			}
			out = append(out, p)
		}
	}
	return out, errors.Join(errs...)
}

func isFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}
