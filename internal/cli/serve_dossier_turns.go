package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/dossiers"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/dossier"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/turn"
)

// dossierRetryEvery is how often the closing calls and compactions the daily cap deferred are tried again.
const dossierRetryEvery = time.Hour

// dossierChecks bounds the turn ends waiting for their dossier check.
const dossierChecks = 256

// grantGrace is how long after its launch a session's start still counts as that launch's: a later
// start is a resume, which the offered resume line runs without the grant.
const grantGrace = 2 * time.Minute

// kindGranted is the loop event of a session launched with its unit's dossier granted; Key is the
// session's handle.
const kindGranted = "dossier_granted"

// serveDossierTurns wires the dossiers into the turns of this serve pass: each session or headless
// turn opens its unit's dossier, each turn end checks and commits it, and, unless --no-jobs, the
// units' open work drives the closing calls and the reopening. Every pass rewires them on its own
// context and config; the passes share one lock on the dossiers' git work.
func (e *Env) serveDossierTurns(ctx context.Context, svc *dossiers.Service, db *sqlite.DB, cfg config.Config, logf func(string, ...any)) {
	if svc == nil {
		e.dossierTurns, e.dossierCalls, e.dossierGrants = nil, nil, nil
		return
	}
	if e.dossierMu == nil {
		e.dossierMu = &sync.Mutex{}
	}
	pages := e.dossierPages(svc.Policy)
	events := sqlite.New(db)
	record := func(ctx context.Context, ev port.Event) error { return events.RecordEvents(ctx, ev) }
	t := &dossiers.Turns{Dossiers: svc, Pages: pages, Log: logf, Events: record, Serial: e.dossierMu}
	calls := &dossiers.Calls{Turns: t, Asker: lazyAsker{e: e, cfg: cfg, dir: filepath.Join(e.StateDir, "dossier-calls")},
		Caps:  dossiers.Caps{PerCallUSD: cfg.Headless.PerCallUSD, PerDayUSD: cfg.Headless.PerDayUSD, Tier: cfg.Headless.Tier},
		Spent: storedSpend(db),
		Record: func(ctx context.Context, u unit.Unit, cost float64) error {
			return record(ctx, port.Event{Unit: u, Kind: dossiers.KindCall, Key: randomKey(), At: e.Now(), Data: strconv.FormatFloat(cost, 'f', -1, 64)})
		}}
	ledger := e.itemsLedger(db, cfg, logf)
	res := &dossiers.Resolution{Calls: calls, Log: logf,
		Items: func(ctx context.Context, u unit.Unit) ([]items.Item, error) { return ledger.Items(ctx, u) }}
	e.dossierTurns, e.dossierCalls = t, calls
	e.dossierGrants = func(ctx context.Context, u unit.Unit) (map[string]time.Time, error) {
		return db.UnitEventKeys(ctx, u, kindGranted)
	}
	e.recordGrant = func(ctx context.Context, u unit.Unit, handle, path string) error {
		return record(ctx, port.Event{Unit: u, Kind: kindGranted, Key: handle, At: e.Now(), Data: path})
	}
	if e.Turns != nil {
		q := make(chan port.Event, dossierChecks)
		e.Turns.Pub = turnEnds{ctx: ctx, next: e.Turns.Pub, q: q, log: logf}
		go e.checkTurnEnds(ctx, t, q, res, logf)
	}
	if e.noJobs || e.changes == nil {
		return
	}
	go func() {
		lctx, scope := meter.Start(ctx, meter.Loop("dossier-resolution"))
		defer scope.End()
		res.Watch(lctx, e.changes, dossierRetryEvery)
	}()
}

// turnEnds hands each turn_ended event to the dossier checks, never blocking the publisher.
type turnEnds struct {
	ctx  context.Context
	next port.Publisher
	q    chan<- port.Event
	log  func(string, ...any)
}

func (p turnEnds) Publish(evs ...port.Event) {
	if p.next != nil {
		p.next.Publish(evs...)
	}
	for _, ev := range evs {
		if ev.Kind != turns.KindTurnEnded || ev.Unit.IsZero() || p.ctx.Err() != nil {
			continue
		}
		select {
		case p.q <- ev:
		default:
			p.log("dossiers: %s's turn end waits for its next turn's check: the queue is full", ev.Unit.Label())
		}
	}
}

// checkTurnEnds checks the dossier of each ended turn's unit, commits it as that turn, and compacts it
// when its log passed the threshold.
func (e *Env) checkTurnEnds(ctx context.Context, t *dossiers.Turns, q <-chan port.Event, res *dossiers.Resolution, logf func(string, ...any)) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-q:
			message := "turn"
			round, _, _ := strings.Cut(ev.Key, ":")
			if b, ok, err := e.Turns.Store.BookOf(ctx, turns.Key{Turn: round}); err == nil && ok {
				if tu, ok := b.ByID(round); ok {
					message = fmt.Sprintf("turn %d", tu.Seq)
				}
			}
			if _, err := t.Check(ctx, ev.Unit, message); err != nil && ctx.Err() == nil {
				logf("dossier of %s: checking %s: %v", ev.Unit.Label(), message, err)
			}
			if err := res.Compact(ctx, ev.Unit); err != nil && ctx.Err() == nil {
				logf("dossier of %s: compacting: %v", ev.Unit.Label(), err)
			}
		}
	}
}

// dossierGrant opens u's dossier for a session about to launch and grants it: read everywhere, write
// under the dossiers' write directories. None without the dossiers or a unit, or when it cannot open.
func (e *Env) dossierGrant(ctx context.Context, u unit.Unit) []turns.Dir {
	if e.dossierTurns == nil || u.IsZero() {
		return nil
	}
	d, err := e.dossierTurns.Open(ctx, u)
	if err != nil {
		e.dossierTurns.Log("dossier of %s: opening it for the session: %v", u.Label(), err)
		return nil
	}
	return []turns.Dir{{Path: d.Path, Write: append([]string(nil), dossiers.WriteDirs...)}}
}

// dossierReader is the session turns' dossier reader; nil without the dossiers.
func (e *Env) dossierReader() turn.DossierReader {
	if e.dossierTurns == nil {
		return nil
	}
	return sessionDossiers{e}
}

// headlessTurnBuilder is the turn builder of a headless call, whose prompt names the dossier read-only.
func (e *Env) headlessTurnBuilder(cfg config.Config) turn.Builder {
	b := e.turnBuilder(cfg)
	b.Dossiers = nil
	return b
}

type sessionDossiers struct{ e *Env }

// Dossier opens u's dossier for a session turn. The session is granted it only when every open session
// of u was launched by the loop with the grant and has not started again since (a resume): the turn
// may reach any of them.
func (s sessionDossiers) Dossier(ctx context.Context, u unit.Unit) (turn.Dossier, bool, error) {
	t := s.e.dossierTurns
	d, err := t.Open(ctx, u)
	if err != nil {
		t.Log("dossier of %s: opening it for the turn: %v", u.Label(), err)
		return turn.Dossier{}, false, nil
	}
	out := turn.Dossier{Path: d.Path}
	if s.e.Turns == nil || s.e.dossierGrants == nil {
		return out, true, nil
	}
	sessions, err := s.e.Turns.Registry.Sessions(ctx)
	if err != nil {
		return out, true, nil
	}
	grants, err := s.e.dossierGrants(ctx, u)
	if err != nil {
		return out, true, nil
	}
	out.Granted = true
	for _, sess := range sessions {
		if sess.Unit != u || !sess.ClosedAt.IsZero() {
			continue
		}
		at, ok := grants[sess.ID]
		if !ok || sess.StartedAt.After(at.Add(grantGrace)) {
			out.Granted = false
		}
	}
	return out, true, nil
}

// grantLaunched records that the session handle, launched for u, was granted u's dossier: the launch
// line opened it just before.
func (e *Env) grantLaunched(ctx context.Context, u unit.Unit, handle string) {
	if e.dossierTurns == nil || e.recordGrant == nil || u.IsZero() {
		return
	}
	d, ok, err := e.dossierTurns.Dossiers.Get(ctx, dossier.Owner{Unit: u})
	if err != nil || !ok || d.Status != dossier.Active {
		return
	}
	if err := e.recordGrant(context.WithoutCancel(ctx), u, handle, d.Path); err != nil {
		e.dossierTurns.Log("dossier of %s: recording the grant of %s: %v", u.Label(), handle, err)
	}
}

// storedSpend is the day's headless spend as stored: the headless turns' and the dossier calls'.
func storedSpend(db *sqlite.DB) func(ctx context.Context, day time.Time) (float64, error) {
	turns := headlessSpent(db)
	return func(ctx context.Context, day time.Time) (float64, error) {
		got, err := turns(ctx, day)
		if err != nil {
			return 0, err
		}
		from := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
		calls, err := db.DossierSpent(ctx, from, from.AddDate(0, 0, 1))
		return got + calls, err
	}
}

func randomKey() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// headlessDossier opens u's dossier for a headless call, which reads it.
func (e *Env) headlessDossier(ctx context.Context, u unit.Unit) (string, bool) {
	if e.dossierTurns == nil {
		return "", false
	}
	d, err := e.dossierTurns.Open(ctx, u)
	if err != nil {
		e.dossierTurns.Log("dossier of %s: opening it for a headless turn: %v", u.Label(), err)
		return "", false
	}
	return d.Path, true
}

func (e *Env) headlessLogged(ctx context.Context, u unit.Unit, seq int64, log string) {
	if e.dossierTurns == nil {
		return
	}
	if err := e.dossierTurns.HeadlessLog(ctx, u, seq, log); err != nil {
		e.dossierTurns.Log("dossier of %s: writing the log of headless turn %d: %v", u.Label(), seq, err)
	}
}

// withDossierSpend is the headless turns' spend with the dossier calls', stored and running, so both
// share one daily cap.
func (e *Env) withDossierSpend(db *sqlite.DB) func(ctx context.Context, day time.Time) (float64, error) {
	stored := storedSpend(db)
	return func(ctx context.Context, day time.Time) (float64, error) {
		got, err := stored(ctx, day)
		if err != nil || e.dossierCalls == nil {
			return got, err
		}
		return got + e.dossierCalls.Reserved(), nil
	}
}
