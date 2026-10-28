package store

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	sdk "github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/pr"
)

// Host is where the events of the store's pull requests live: the host's
// event log, written and read through the broker.
type Host interface {
	RecordEvents(ctx context.Context, events []src.Event) error
	Events(ctx context.Context, q src.EventQuery) ([]src.Event, error)
}

// UseHost makes h the host the store records its events to and reads them
// from; a store with no host refuses every write that derives an event.
func (db *DB) UseHost(h Host) {
	db.useMu.Lock()
	defer db.useMu.Unlock()
	db.host = h
}

// UseClock makes now the time of a scope flip no write dates.
func (db *DB) UseClock(now func() time.Time) {
	db.useMu.Lock()
	defer db.useMu.Unlock()
	db.clock = now
}

func (db *DB) hostOf() (Host, error) {
	db.useMu.Lock()
	defer db.useMu.Unlock()
	if db.host == nil {
		return nil, fmt.Errorf("github store: no host to keep its events")
	}
	return db.host, nil
}

func (db *DB) now() time.Time {
	db.useMu.Lock()
	defer db.useMu.Unlock()
	if db.clock == nil {
		return time.Now()
	}
	return db.clock()
}

// wireEvent is e as the broker carries it. Its data travels as the text
// the host's event log keeps, which need not be JSON.
func wireEvent(e pr.Event) src.Event {
	out := src.Event{Unit: e.PR.Unit(), Kind: e.Kind, Key: e.Key, Actor: e.Actor, At: e.At}
	if e.Data != "" {
		out.Data = json.RawMessage(e.Data)
	}
	return out
}

// RecordEvents records evs on the host. A keyed event is kept once per
// pull request, kind and key.
func (db *DB) RecordEvents(ctx context.Context, evs []pr.Event) error {
	if len(evs) == 0 {
		return nil
	}
	if err := timed(evs); err != nil {
		return err
	}
	h, err := db.hostOf()
	if err != nil {
		return err
	}
	wire := make([]src.Event, len(evs))
	for i, e := range evs {
		wire[i] = wireEvent(e)
	}
	return h.RecordEvents(ctx, wire)
}

// HostEvents reads the host's events of refs of kinds, every kind when
// empty, at or after since, oldest first.
func (db *DB) HostEvents(ctx context.Context, refs []pr.Ref, since time.Time, kinds ...string) ([]pr.Event, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	h, err := db.hostOf()
	if err != nil {
		return nil, err
	}
	units := make([]sdk.Unit, len(refs))
	for i, r := range refs {
		units[i] = r.Unit()
	}
	got, err := h.Events(ctx, src.EventQuery{Units: units, Kinds: kinds, Since: since})
	if err != nil {
		return nil, err
	}
	out := make([]pr.Event, 0, len(got))
	for _, e := range got {
		ref, ok := pr.RefOf(e.Unit)
		if !ok {
			continue
		}
		out = append(out, pr.Event{PR: ref, Kind: e.Kind, Key: e.Key, Actor: e.Actor, At: e.At, Data: string(e.Data)})
	}
	return out, nil
}

// LastEvent is the newest event of kind on ref.
func (db *DB) LastEvent(ctx context.Context, ref pr.Ref, kind string) (pr.Event, bool, error) {
	evs, err := db.HostEvents(ctx, []pr.Ref{ref}, time.Time{}, kind)
	if err != nil || len(evs) == 0 {
		return pr.Event{}, false, err
	}
	return evs[len(evs)-1], true, nil
}

func timed(evs []pr.Event) error {
	for _, e := range evs {
		if e.At.IsZero() {
			return fmt.Errorf("event %s on %s has no time", e.Kind, e.PR)
		}
	}
	return nil
}

type scope struct {
	reasons  []string
	ws       string
	open     bool
	followed bool
	ended    time.Time
}

// scopeOf is the scope of a stored row: followed when it is open and
// PR.Followed holds; ended is when a merged or closed one finished.
func scopeOf(reasons []string, ws, state, ended string) (scope, error) {
	end, err := parseTime(ended)
	if err != nil {
		return scope{}, err
	}
	return scope{reasons: reasons, ws: ws, open: state == pr.StateOpen, ended: end}.with(reasons, ws), nil
}

// with is sc with reasons and the workspace ws.
func (sc scope) with(reasons []string, ws string) scope {
	sc.reasons, sc.ws = reasons, ws
	sc.followed = sc.open && pr.PR{Reasons: reasons, Workspace: pr.Workspace{ID: ws}}.Followed()
	return sc
}

// scopes reads the scope of each of refs.
func scopes(ctx context.Context, tx reader, refs []pr.Ref) (map[pr.Ref]scope, error) {
	out := map[pr.Ref]scope{}
	for chunk := range slices.Chunk(refs, prReadChunk) {
		marks, args := refValues(chunk)
		rows, err := tx.QueryContext(ctx, refsIn(`SELECT owner, repo, number, reasons, ws_id, state, iif(merged_at != '', merged_at, closed_at) FROM pull_requests
			WHERE (owner, repo, number)`, marks), args...)
		if err != nil {
			return nil, err
		}
		err = func() error {
			defer rows.Close()
			for rows.Next() {
				var ref pr.Ref
				var reasons, ws, state, ended string
				if err := rows.Scan(&ref.Owner, &ref.Repo, &ref.Number, &reasons, &ws, &state, &ended); err != nil {
					return err
				}
				list, err := splitList(reasons)
				if err != nil {
					return err
				}
				if out[ref], err = scopeOf(list, ws, state, ended); err != nil {
					return err
				}
			}
			return rows.Err()
		}()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// flipLog is what the host holds of pull requests' scope flips: the kind
// of each one's last flip and the time of its last followed event.
type flipLog struct {
	last       map[pr.Ref]string
	lastAt     map[pr.Ref]time.Time
	followedAt map[pr.Ref]time.Time
}

// saw adds e to the log when it is a flip.
func (l *flipLog) saw(e pr.Event) {
	if e.Kind != pr.KindFollowed && e.Kind != pr.KindUnfollowed {
		return
	}
	if l.last == nil {
		l.last, l.lastAt, l.followedAt = map[pr.Ref]string{}, map[pr.Ref]time.Time{}, map[pr.Ref]time.Time{}
	}
	if !e.At.Before(l.lastAt[e.PR]) {
		l.last[e.PR], l.lastAt[e.PR] = e.Kind, e.At
	}
	if e.Kind == pr.KindFollowed && e.At.After(l.followedAt[e.PR]) {
		l.followedAt[e.PR] = e.At
	}
}

// wants says whether e, a flip, is one the host lacks: a flip of the kind
// the host's last one already has is a retry of a write that failed after
// the host took it.
func (l *flipLog) wants(e pr.Event) bool { return l.last[e.PR] != e.Kind }

// recordFlipsOf records on the host, before the write that makes them, the
// flip of each of refs the write turns from its stored scope, zero when it
// has no row, into what change says, at at, else now; change says false
// for a ref the write leaves.
func (db *DB) recordFlipsOf(ctx context.Context, refs []pr.Ref, at time.Time, change func(pr.Ref, scope) (scope, bool, error)) error {
	before, err := scopes(ctx, db.db, refs)
	if err != nil {
		return err
	}
	var flipped []pr.Ref
	after := map[pr.Ref]scope{}
	for _, ref := range refs {
		was := before[ref]
		now, ok, err := change(ref, was)
		if err != nil {
			return err
		}
		if ok && now.followed != was.followed {
			flipped = append(flipped, ref)
			after[ref] = now
		}
	}
	if len(flipped) == 0 {
		return nil
	}
	slices.SortFunc(flipped, pr.Compare)
	held, err := db.HostEvents(ctx, flipped, time.Time{}, pr.KindFollowed, pr.KindUnfollowed)
	if err != nil {
		return err
	}
	var log flipLog
	for _, e := range held {
		log.saw(e)
	}
	at = at.UTC()
	if at.IsZero() {
		at = db.now().UTC()
	}
	var evs []pr.Event
	for _, ref := range flipped {
		e := flip(ref, before[ref], after[ref], at, log.followedAt[ref])
		if log.wants(e) {
			evs = append(evs, e)
		}
	}
	return db.RecordEvents(ctx, evs)
}

// flip is the followed or unfollowed event of ref's scope going from was to
// now at at. A merge or close is dated when it happened, but never before
// followedAt, the latest followed event, so an interval in the loop never
// ends before it starts: a merge GitHub dates before the loop first saw the
// pull request ends it at once.
func flip(ref pr.Ref, was, now scope, at, followedAt time.Time) pr.Event {
	if now.followed {
		return pr.Event{PR: ref, Kind: pr.KindFollowed, Key: formatTime(at), At: at, Data: strings.Join(now.reasons, ",")}
	}
	end := at
	if !now.ended.IsZero() {
		end = now.ended.UTC()
	}
	if followedAt.After(end) {
		end = followedAt
	}
	return pr.Event{PR: ref, Kind: pr.KindUnfollowed, Key: formatTime(end), At: end, Data: strings.Join(was.reasons, ",")}
}
