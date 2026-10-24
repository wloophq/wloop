package cli

import (
	"context"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/config"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// serveTurnLedger gives the daemon's turns its one items ledger: a record's
// ops apply to it, and turns list the unit's items and op outcomes from it.
func (e *Env) serveTurnLedger(db *sqlite.DB, cfg config.Config, logf func(string, ...any)) {
	if e.Turns == nil || e.Turns.Items != nil {
		return
	}
	e.Turns.Items = turnLedger{Applier: e.itemsLedger(db, cfg, logf), log: db.Items()}
}

type turnLedger struct {
	coreitems.Applier
	log coreitems.Reader
}

func (l turnLedger) Log(ctx context.Context, u unit.Unit, since time.Time) ([]coreitems.LogEntry, error) {
	return l.log.Log(ctx, u, since)
}
