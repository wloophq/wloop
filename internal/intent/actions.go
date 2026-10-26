package intent

import (
	"io/fs"

	"github.com/wloophq/wloop/internal/profile"
)

// BuiltinAction is a built-in action's texts, from its actions.toml table.
type BuiltinAction struct {
	Label, Summary              string
	Note, Warning, Confirm, Why string
	Input                       struct{ Label, Help string }
}

// loadActions reads actions.toml from bundled and the user's layers, by
// catalog id.
func loadActions(layers []profile.Layer, bundled fs.FS) (map[string]BuiltinAction, Problems) {
	return nil, nil
}
