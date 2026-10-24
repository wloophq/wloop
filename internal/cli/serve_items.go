package cli

import (
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/config"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// itemsLedger is the daemon's one items ledger, built on first use; it refuses writes to a
// disabled plugin's units.
func (e *Env) itemsLedger(db *sqlite.DB, cfg config.Config, logf func(string, ...any)) *coreitems.Ledger {
	if e.items != nil {
		return e.items
	}
	e.items = &coreitems.Ledger{
		Store:   db.Items(),
		Links:   db.Links(),
		Enabled: func(p unit.ProviderID) bool { return cfg.PluginEnabled(string(p)) },
		Clock:   clockFunc(e.Now),
		Log:     logf,
	}
	return e.items
}
