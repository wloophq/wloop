package intent

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"

	dunit "github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/profile"
)

// DefaultsFile is the profile file of the manifests' defaults, read from
// the bundled profile and then each layer, a later layer's key winning.
const DefaultsFile = "defaults.toml"

// manifestDefaults are what a manifest that leaves a key out gets.
type manifestDefaults struct {
	// Provider is an intent's provider when its manifest names none.
	Provider dunit.ProviderID `toml:"provider"`
}

func readDefaults(layers []profile.Layer, bundled fs.FS) (manifestDefaults, Problems) {
	var out manifestDefaults
	var problems Problems
	merge := func(file string, src []byte) {
		var d manifestDefaults
		if err := toml.Unmarshal(src, &d); err != nil {
			problems = append(problems, Problem{File: file, Msg: err.Error()})
			return
		}
		s := &sheet{file: file, at: positions(src)}
		s.addGrammar(grammar(builtin.SurfaceDefaults, src, file != DefaultsFile))
		if len(s.problems) > 0 {
			problems = append(problems, s.problems...)
			return
		}
		if d.Provider != "" {
			out.Provider = d.Provider
		}
	}
	if bundled != nil {
		if src, err := fs.ReadFile(bundled, DefaultsFile); err == nil {
			merge(DefaultsFile, src)
		}
	}
	for _, l := range layers {
		if l.Dir == "" {
			continue
		}
		file := filepath.Join(l.Dir, DefaultsFile)
		src, err := os.ReadFile(file)
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			problems = append(problems, Problem{File: file, Msg: err.Error()})
		default:
			merge(file, src)
		}
	}
	return out, problems
}
