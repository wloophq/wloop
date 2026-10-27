package intent

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"text/template"

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
	// Refusal is what a [[refuse]] case whose why renders empty says.
	Refusal string `toml:"refusal"`
	// SessionName is an intent's session.name when its manifest has none.
	SessionName string `toml:"session_name"`
}

// checkSessionName renders src over every name fact, so a session_name
// that cannot render is reported in defaults.toml rather than in every
// manifest that takes it.
func checkSessionName(src string) error {
	t, err := template.New("session_name").Option("missingkey=error").Parse(src)
	if err == nil {
		err = t.Execute(io.Discard, sample(builtin.Env(builtin.NameSlot, nil)))
	}
	return err
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
		s.addGrammar(grammar(builtin.SurfaceDefaults, src))
		if len(s.problems) > 0 {
			problems = append(problems, s.problems...)
			return
		}
		if d.Provider != "" {
			out.Provider = d.Provider
		}
		if d.Refusal != "" {
			out.Refusal = d.Refusal
		}
		if d.SessionName != "" {
			if err := checkSessionName(d.SessionName); err != nil {
				problems = append(problems, Problem{File: file, Line: 1, Col: 1, Field: "session_name", Msg: err.Error()})
				return
			}
			out.SessionName = d.SessionName
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
