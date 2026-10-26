package cli

import (
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/source"
)

// serveWiring is what one part of the daemon's composition reads and adds
// to: the daemon's deps and the sweep engine's, over the shared handles.
type serveWiring struct {
	DB      *sqlite.DB
	Cfg     config.Config
	Modules []source.Module
	Log     func(format string, args ...any)
	Deps    *daemon.Deps
	Engine  *engineDeps
}

// wireParts are the parts serveAllDeps runs once, in order, before it
// builds the engine.
func (e *Env) wireParts() []func(w *serveWiring) {
	return []func(w *serveWiring){
		e.wireSubjects,
		e.wireDrafts,
		e.wireTurnStart,
		e.wireProse,
		e.wireBriefs,
		e.wireWorkflows,
		e.wireCapabilities,
		e.wireDims,
	}
}
