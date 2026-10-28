package intent

import (
	"io/fs"

	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/profile"
)

// GatesFile is the profile file that words the gates.
const GatesFile = "gates.toml"

// loadGates checks gates.toml in bundled and the user's layers against its
// grammar.
func loadGates(layers []profile.Layer, bundled fs.FS) Problems {
	problems, _ := readLayered(layers, bundled, GatesFile, func(file string, src []byte) Problems {
		_, problems, _, complete := decode[map[string]any](file, src)
		if !complete {
			return problems
		}
		s := &sheet{file: file, at: positions(src), problems: problems}
		s.addGrammar(grammar(builtin.SurfaceGates, src))
		return s.problems
	})
	return problems
}
