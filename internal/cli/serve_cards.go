package cli

import (
	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel"
	cardsjob "github.com/wloophq/wloop/internal/readmodel/cards"
)

// withCards is d serving the stored cards, stale for a disabled plugin or
// one that is down, and running the cards job over the built modules with a
// card or items and over the plugins the host runs, whose cards are their
// reports'. The job runs under --no-jobs too: it reads and writes only the
// store.
func (e *Env) withCards(d daemon.Deps, db *sqlite.DB, cfg config.Config, mods []pluginModule, logf func(string, ...any)) daemon.Deps {
	d.Cards = cardsjob.Served(db.Cards(), e.cardsFresh(d.PluginEnabled))
	providers := map[unit.ProviderID]cardsjob.Provider{}
	for _, m := range mods {
		if m.Card != nil || m.Items != nil {
			providers[m.ID] = cardsjob.Provider{Card: m.Card, Items: m.Items}
		}
	}
	store := inventory.New(db)
	for _, p := range e.reportedPlugins(cfg, mods) {
		providers[p.id] = cardsjob.Provider{Card: readmodel.NewReported(e.reportedSource(p, store, readmodel.RowDeps{})).Card}
	}
	if len(providers) == 0 {
		return d
	}
	ledger := e.itemsLedger(db, cfg, logf)
	d.CardsJob = &cardsjob.Job{Store: db.Cards(), Ledger: ledger, Open: ledger, Providers: providers, Clock: clockFunc(e.Now), Log: logf}
	return d
}

// cardsFresh says a provider's cards are fresh: its plugin is enabled and,
// when the host runs it, up; nil while every one is.
func (e *Env) cardsFresh(enabled func(unit.ProviderID) bool) func(unit.ProviderID) bool {
	if e.pluginKind == nil {
		return enabled
	}
	snaps := e.pluginKind.Snapshots
	return func(p unit.ProviderID) bool {
		return (enabled == nil || enabled(p)) && !snaps.Stale(unit.Unit{Provider: p})
	}
}
