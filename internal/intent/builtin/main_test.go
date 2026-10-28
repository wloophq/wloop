package builtin

import (
	"io/fs"
	"os"
	"path"
	"testing"

	"github.com/wloophq/wloop/pkg/plugin/manifest"
	"github.com/wloophq/wloop/plugins"
)

// TestMain installs the bundled plugins' vocabulary, as the composition
// root does, since most rows the tests read are theirs.
func TestMain(m *testing.M) {
	v, err := Merge(bundledSources()...)
	if err != nil {
		panic(err)
	}
	Install(v)
	os.Exit(m.Run())
}

func bundledSources() []*manifest.Source {
	dirs, err := fs.ReadDir(plugins.Manifests, ".")
	if err != nil {
		panic(err)
	}
	var out []*manifest.Source
	for _, d := range dirs {
		raw, err := fs.ReadFile(plugins.Manifests, path.Join(d.Name(), manifest.File))
		if err != nil {
			panic(err)
		}
		m, err := manifest.Parse(raw)
		if err != nil {
			panic(err)
		}
		s, err := m.Source()
		if err != nil {
			panic(err)
		}
		out = append(out, s)
	}
	return out
}
