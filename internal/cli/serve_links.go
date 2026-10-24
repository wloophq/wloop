package cli

import (
	"context"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/links"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/source"
)

// linksRuntime names the resolver among the daemon's runtimes.
const linksRuntime unit.ProviderID = "links"

// linkWiring is the daemon's link store and unit registry as the sources
// see them, and the resolver over them.
type linkWiring struct {
	e     *Env
	db    *sqlite.DB
	cfg   config.Config
	logf  func(string, ...any)
	store port.LinkStore
	units atomic.Pointer[unit.Registry]
}

// linkDeps fills sd's registry and link store; the registry is the daemon's
// once serve registers the resolver.
func (e *Env) linkDeps(db *sqlite.DB, cfg config.Config, logf func(string, ...any), sd *source.Deps) *linkWiring {
	w := &linkWiring{e: e, db: db, cfg: cfg, logf: logf, store: db.Links()}
	sd.Units, sd.Links = w.units.Load, w.store
	return w
}

// serve is d with the resolver as a runtime: woken by the source events and
// links noted on units, it resolves the open items linked to them. It runs
// only when an enabled plugin reads unit events.
func (w *linkWiring) serve(d daemon.Deps, mods []source.Module) daemon.Deps {
	w.units.Store(d.Units)
	events := map[unit.ProviderID]links.EventReader{}
	for _, m := range mods {
		if m.UnitEvents != nil {
			events[m.ID] = m.UnitEvents
		}
	}
	if len(events) == 0 || w.e.changes == nil {
		return d
	}
	r := &links.Resolver{Links: w.store, Events: events, Ledger: w.e.itemsLedger(w.db, w.cfg, w.logf), Clock: clockFunc(w.e.Now), Log: w.logf}
	changes := w.e.changes
	d.Runtimes = append(d.Runtimes, daemon.PluginRuntime{ID: linksRuntime, Run: func(ctx context.Context) error {
		return resolveNoted(ctx, r.Run, w.db.LinkTargets, changes, w.logf)
	}})
	return d
}

// linksRetry is how long the resolver waits before running units again after
// a failed run or catch-up read.
var linksRetry = 30 * time.Second

// resolveNoted runs the resolver until ctx ends: first over every link
// target from seed, which catches up on events noted while it was not
// listening, then over the units noted with source events or links. Units of
// a failed run run again after linksRetry. The subscription only hands the
// units over.
func resolveNoted(ctx context.Context, run func(context.Context, []unit.Unit) (int, error), seed func(context.Context) ([]unit.Unit, error),
	changes port.Changes, logf func(string, ...any)) error {
	var mu sync.Mutex
	noted := map[unit.Unit]bool{}
	add := func(units []unit.Unit) {
		mu.Lock()
		defer mu.Unlock()
		for _, u := range units {
			noted[u] = true
		}
	}
	wake := make(chan struct{}, 1)
	defer changes.Subscribe(func(c port.Change) {
		if !c.Has(port.TopicSourceEvents|port.TopicLinks) || len(c.Units) == 0 {
			return
		}
		add(c.Units)
		select {
		case wake <- struct{}{}:
		default:
		}
	})()
	seeded := false
	var retry <-chan time.Time
	for {
		if !seeded {
			targets, err := seed(ctx)
			if err != nil {
				logf("links: reading the link targets: %v; again in %v", err, linksRetry)
				retry = time.After(linksRetry)
			} else {
				add(targets)
				seeded = true
			}
		}
		mu.Lock()
		units := slices.Collect(maps.Keys(noted))
		clear(noted)
		mu.Unlock()
		if len(units) > 0 {
			lctx, scope := meter.Start(ctx, meter.Loop("links"))
			n, err := run(lctx, units)
			scope.End()
			if n > 0 {
				logf("links: resolved %d linked items", n)
			}
			if err != nil && ctx.Err() == nil {
				logf("links: %v; %d units again in %v", err, len(units), linksRetry)
				add(units)
				retry = time.After(linksRetry)
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-wake:
		case <-retry:
			retry = nil
		}
	}
}
