package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/config"
	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/source"
)

// entries is the daemon's one entries service, built on first use; its unit facts come from the stored cards.
func (e *Env) entries(db *sqlite.DB, cfg config.Config, its coregtd.Items) *coregtd.Service {
	if e.gtd == nil {
		e.gtdMe = &identity{}
		e.pluginsOff = &atomic.Pointer[[]unit.ProviderID]{}
		off := func() []unit.ProviderID { return offProviders(cfg, e.pluginsOff.Load()) }
		enabled := func(p unit.ProviderID) bool { return !slices.Contains(off(), p) }
		facts := coregtd.CardFacts{Cards: db.Cards(), Enabled: enabled, Me: e.gtdMe.get}
		e.gtd = coregtd.New(coregtd.Deps{Store: db.Entries(), Items: its, Facts: facts, Clock: clockFunc(e.Now),
			StaleWaitingDays: cfg.GTD.StaleWaitingDays, Disabled: off})
	}
	return e.gtd
}

// offProviders are the plugins cfg disables and those the daemon serves disabled (built), once serveGTD
// knows them.
func offProviders(cfg config.Config, built *[]unit.ProviderID) []unit.ProviderID {
	var out []unit.ProviderID
	for _, id := range cfg.PluginSwitches() {
		if !cfg.PluginEnabled(id) {
			out = append(out, unit.ProviderID(id))
		}
	}
	if built != nil {
		for _, p := range *built {
			if !slices.Contains(out, p) {
				out = append(out, p)
			}
		}
	}
	return out
}

// serveGTD serves the entries on d: the user's identity from every built module's Identity, and the units a
// full Sync covers. The daemon's Sync worker also runs under --no-jobs, since it only reads and writes the store.
func (e *Env) serveGTD(d daemon.Deps, db *sqlite.DB, cfg config.Config, mods []source.Module, logf func(string, ...any)) daemon.Deps {
	ledger := e.itemsLedger(db, cfg, logf)
	g := e.entries(db, cfg, ledger)
	var off []unit.ProviderID
	for _, p := range d.Plugins {
		if !p.Enabled {
			off = append(off, p.ID)
		}
	}
	e.pluginsOff.Store(&off)
	if e.Turns != nil {
		e.Turns.Results = e.newEntryResults(g, mods)
	}
	me := e.gtdMe
	d.GTD = g
	d.GTDMe = func(ctx context.Context) (bool, error) {
		got, err := meOf(ctx, mods)
		grew := me.set(got)
		g.SetMe(me.get())
		return grew, err
	}
	d.GTDUnits = func(ctx context.Context) ([]unit.Unit, error) { return syncUnits(ctx, db.Entries(), ledger) }
	return d
}

// meOf is the user over every module with an Identity: each one's key and aliases. A module that cannot say
// yet is left out and named in the error.
func meOf(ctx context.Context, mods []source.Module) (gtd.Me, error) {
	var me gtd.Me
	var errs []error
	var codecs []unit.Codec
	for _, m := range mods {
		codecs = append(codecs, m.Codec)
	}
	me.Codecs = unit.NewRegistry(codecs...)
	for _, m := range mods {
		if m.Identity == nil {
			continue
		}
		p, err := m.Identity(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", m.ID, err))
			continue
		}
		for _, k := range append([]string{p.Key}, p.Aliases...) {
			if k != "" && !slices.Contains(me.Keys, k) {
				me.Keys = append(me.Keys, k)
			}
		}
	}
	return me, errors.Join(errs...)
}

// identity is the user's identity as far as the plugins said it; it only grows while the daemon runs.
type identity struct {
	mu sync.RWMutex
	me gtd.Me
}

func (i *identity) get() gtd.Me {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return gtd.Me{Keys: slices.Clone(i.me.Keys), Codecs: i.me.Codecs}
}

func (i *identity) set(me gtd.Me) (grew bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if me.Codecs != nil {
		i.me.Codecs = me.Codecs
	}
	for _, k := range me.Keys {
		if !slices.Contains(i.me.Keys, k) {
			i.me.Keys, grew = append(i.me.Keys, k), true
		}
	}
	return grew
}

// syncUnits are the units with an open item or a live entry.
func syncUnits(ctx context.Context, entries coregtd.Reader, ledger *coreitems.Ledger) ([]unit.Unit, error) {
	open, err := ledger.Query(ctx, coreitems.Query{State: items.Open})
	if err != nil {
		return nil, err
	}
	seen := map[unit.Unit]bool{}
	var out []unit.Unit
	add := func(u unit.Unit) {
		if !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	for _, it := range open {
		add(it.Unit)
	}
	live, err := entries.Entries(ctx, gtd.Query{View: gtd.ViewOpen})
	if err != nil {
		return nil, err
	}
	for _, e := range live {
		for _, u := range e.Units {
			add(u)
		}
		for _, r := range e.Items {
			add(r.Unit)
		}
	}
	return out, nil
}
