package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/headless"
	coreplacement "github.com/wloophq/wloop/internal/core/placement"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/daemon"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/placement"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/turn"
)

// serveHeadless serves headless turns on d: their usage goes to turn_usage, an answer to a headless turn's
// question runs it again, and the headless turns a stop cut short end failed before any new one starts.
func (e *Env) serveHeadless(ctx context.Context, d daemon.Deps, db *sqlite.DB, cfg config.Config, mods []pluginModule, logf func(string, ...any)) daemon.Deps {
	if e.Turns == nil || db == nil {
		return d
	}
	ledger := e.itemsLedger(db, cfg, logf)
	svc := &headless.Service{
		Turns:   e.Turns,
		Build:   headlessBuilder{e: e, cfg: cfg},
		Place:   headlessPlacer{e: e, cfg: cfg},
		Asker:   lazyAsker{e: e, cfg: cfg, dir: filepath.Join(e.StateDir, "headless")},
		Ledger:  ledger,
		Outbox:  e.outbox,
		Intents: e.headlessIntent,
		Default: e.headlessDefault,
		Caps:    headless.Caps{PerCallUSD: cfg.Headless.PerCallUSD, PerDayUSD: cfg.Headless.PerDayUSD, Tier: cfg.Headless.Tier},
		Context: ctx,
		Spent:   e.withDossierSpend(db),
		Dossier: e.headlessDossier,
		Logged:  e.headlessLogged,
		Clock:   clockFunc(e.Now),
		Log:     logf,
	}
	e.Turns.Usage = db.Usage()
	e.Turns.Headless = svc.Again
	if !e.headlessSwept {
		e.headlessSwept = true
		if n, err := e.Turns.EndInterruptedHeadless(ctx, cfg.Headless.PerCallUSD); err != nil {
			logf("headless: ending the turns a stop cut short: %v", err)
		} else if n > 0 {
			logf("headless: %d turns a stop cut short ended failed", n)
		}
	}
	d.Headless = svc
	return d
}

// sessionPlacer is the daemon's Placer: the unit's placement, else the neutral repository; an error
// when no workspace manager is enabled to run the session, or the placement cannot be read.
type sessionPlacer struct {
	e   *Env
	cfg config.Config
}

func (p sessionPlacer) SessionPlace(ctx context.Context, u unit.Unit) (string, error) {
	if p.e.WS == nil || p.e.WS.Len() == 0 {
		return "", errors.New("no workspace manager is enabled to run a session (workspace_managers in the config)")
	}
	pl, err := p.e.placements(p.cfg).Place(ctx, u, "")
	switch {
	case errors.Is(err, coreplacement.ErrUnplaced):
		return "a neutral directory with no checkout", nil
	case err != nil:
		return "", err
	case filepath.IsAbs(pl.Checkout):
		return pl.Checkout, nil
	case pl.Checkout == placement.CheckoutMain:
		return "the main checkout of " + pl.Repo, nil
	}
	return "a worktree of " + pl.Repo, nil
}

func (e *Env) headlessIntent(name dturn.Intent) (dintent.Def, bool) {
	l := e.loadedIntents()
	if l == nil {
		return dintent.Def{}, false
	}
	return l.Registry.Get(dintent.Name(name))
}

// headlessDefault is the intent holding the headless.default role on units of p.
func (e *Env) headlessDefault(p unit.ProviderID) (dintent.Def, bool) {
	l := e.loadedIntents()
	if l == nil {
		return dintent.Def{}, false
	}
	return l.Registry.ByRole(builtin.RoleHeadless, p)
}

// headlessSpent is the cost of the headless turns started on the local day of day.
func headlessSpent(db *sqlite.DB) func(ctx context.Context, day time.Time) (float64, error) {
	return func(ctx context.Context, day time.Time) (float64, error) {
		from := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
		return db.HeadlessSpent(ctx, from, from.AddDate(0, 0, 1))
	}
}

// headlessBuilder renders a unit's instructions as a turn by hand would get them.
type headlessBuilder struct {
	e   *Env
	cfg config.Config
}

func (b headlessBuilder) Instructions(ctx context.Context, u unit.Unit, intent dturn.Intent, _ []string) (string, error) {
	r := turn.Request{Intent: turn.Intent(intent), Trigger: turn.ByHand, Unit: u}
	if _, ok := b.e.payloadReaders()[u.Provider]; !ok {
		r.Payload = unitPayload{u}
	}
	_, out, err := b.e.renderTurnBy(ctx, b.cfg, r, b.e.headlessTurnBuilder)
	return out.Text, err
}

// unitPayload is a unit as a headless turn reads it while its provider hands no payload: its key alone.
type unitPayload struct{ u unit.Unit }

func (p unitPayload) Unit() unit.Unit { return p.u }
func (p unitPayload) Arg() string     { return p.u.String() }
func (p unitPayload) Wording() turn.Wording {
	return turn.Wording{Noun: string(p.u.Provider) + " unit", This: "this " + string(p.u.Provider) + " unit", Ref: p.u.Label(), Short: p.u.Key}
}
func (p unitPayload) Fact(string) (any, bool) { return nil, false }

// headlessPlacer is the placed checkout of a unit when it exists: an absolute checkout, or the main
// checkout of the placed repository; a new worktree or no placement is none.
type headlessPlacer struct {
	e   *Env
	cfg config.Config
}

func (p headlessPlacer) Checkout(ctx context.Context, u unit.Unit, item string) (string, bool, error) {
	pl, err := p.e.placements(p.cfg).Place(ctx, u, item)
	switch {
	case errors.Is(err, coreplacement.ErrUnplaced):
		return "", false, nil
	case err != nil:
		return "", false, err
	}
	var dir string
	switch {
	case filepath.IsAbs(pl.Checkout):
		dir = pl.Checkout
	case pl.Checkout == placement.CheckoutMain && p.e.WS != nil:
		repos, err := p.e.WS.Repos(ctx)
		if err != nil {
			return "", false, err
		}
		repo, ok := placedRepo(repos, pl.Repo)
		if !ok {
			return "", false, fmt.Errorf("%s is placed in the repository %q, which %s does not know", u.Label(), pl.Repo, p.e.managerDisplay())
		}
		dir = repo.Path
	default:
		return "", false, nil
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return "", false, fmt.Errorf("%s is placed in %s, which is not a directory", u.Label(), dir)
	}
	return filepath.Clean(dir), true, nil
}

// lazyAsker builds the configured agent's asker at each call, so a daemon started before the agent was
// installed still runs headless turns once it is.
type lazyAsker struct {
	e   *Env
	cfg config.Config
	dir string
}

func (a lazyAsker) Ask(ctx context.Context, q turns.Ask) (turns.Answer, error) {
	asker, err := a.e.asker(a.cfg, a.dir)
	if err != nil {
		return turns.Answer{}, err
	}
	return asker.Ask(ctx, q)
}

// Ready asks the configured agent's asker whether a call can run, when it can tell.
func (a lazyAsker) Ready(ctx context.Context, q turns.Ask) error {
	asker, err := a.e.asker(a.cfg, a.dir)
	if err != nil {
		return err
	}
	if r, ok := asker.(headless.Readier); ok {
		return r.Ready(ctx, q)
	}
	return nil
}
