package cli

import (
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/unit"
	cardsjob "github.com/wloophq/wloop/internal/readmodel/cards"
	"github.com/wloophq/wloop/internal/source"
)

// withCards is d serving the stored cards, stale for a disabled plugin, and
// running the cards job over the built modules with a card or items. The
// job runs under --no-jobs too: it reads and writes only the store.
func (e *Env) withCards(d daemon.Deps, db *sqlite.DB, cfg config.Config, mods []source.Module, logf func(string, ...any)) daemon.Deps {
	d.Cards = cardsjob.Served(db.Cards(), d.PluginEnabled)
	providers := map[unit.ProviderID]cardsjob.Provider{}
	for _, m := range mods {
		if m.Card != nil || m.Items != nil {
			providers[m.ID] = cardsjob.Provider{Card: m.Card, Items: m.Items}
		}
	}
	if len(providers) == 0 {
		return d
	}
	ledger := e.itemsLedger(db, cfg, logf)
	d.CardsJob = &cardsjob.Job{Store: db.Cards(), Ledger: ledger, Open: ledger, Providers: providers, Clock: clockFunc(e.Now), Log: logf}
	return d
}
