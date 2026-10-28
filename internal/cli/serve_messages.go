package cli

import (
	"context"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/daemon"
)

// serveMessages serves the Inbox messages on d and keeps them in step with the turn books and the ledger's
// proposals until ctx ends; it also runs under --no-jobs, since it only reads and writes the local store.
func (e *Env) serveMessages(ctx context.Context, d daemon.Deps, db *sqlite.DB, cfg config.Config, logf func(string, ...any)) daemon.Deps {
	if e.Turns == nil {
		return d
	}
	ledger := e.itemsLedger(db, cfg, logf)
	tasks := e.newTasks(db, cfg, ledger)
	if e.messages == nil {
		e.messages = &messages.Service{Store: inventory.NewMessages(db), Books: openPRBooks{sqlite.New(db), inventory.New(db)}, Turns: e.Turns,
			Ledger: ledger, Tasks: tasks, Intents: registryPort{e}, Clock: clockFunc(e.Now), Log: logf}
	}
	d.Tasks, d.Messages = tasks, e.messages
	d.Gates = e.newGates(db, tasks, logf)
	d.GateReader = e.gateReader
	if e.changes != nil {
		e.background.Go(func() { e.messages.Run(ctx, e.changes) })
		e.background.Go(func() { reconcileEnded(ctx, e.messages, e.changes, logf) })
	}
	return d
}
