package cli

import (
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/memory"
)

// serveMemory builds, once per daemon, what the turns of a task learned, which the turns' instructions and
// task_read read; none without the turns.
func (e *Env) serveMemory(db *sqlite.DB, cfg config.Config, logf func(string, ...any)) {
	if e.Turns == nil || e.memory != nil {
		return
	}
	ledger := e.itemsLedger(db, cfg, logf)
	e.memory = &memory.Service{Tasks: db.Tasks(), Merges: db, Books: e.Turns.Store, Items: turnLedger{Applier: ledger, log: db.Items()}}
}
