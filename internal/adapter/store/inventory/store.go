// Package inventory stores the pull requests in scope, their event history
// and the user's overrides in SQLite.
package inventory

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/event"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

const timeLayout = event.TimeLayout

const eventsChunk = 100

type Store struct {
	db    *sqlite.DB
	clock func() time.Time
	host  src.Host
	prs   atomic.Pointer[PRSource]
}

// PRSource is where the store reads the pull requests the GitHub plugin
// reported, which it keeps no copy of.
type PRSource interface {
	PRs(ctx context.Context, refs []github.Ref) (map[github.Ref]dinv.PR, error)
	List(ctx context.Context, open bool) ([]dinv.PR, error)
}

// UsePRs reads the pull requests from prs; until then the store knows none.
func (s *Store) UsePRs(prs PRSource) { s.prs.Store(&prs) }

func (s *Store) reportedPRs(ctx context.Context, refs []github.Ref) (map[github.Ref]dinv.PR, error) {
	prs := s.prs.Load()
	if prs == nil {
		return map[github.Ref]dinv.PR{}, nil
	}
	return (*prs).PRs(ctx, refs)
}

func (s *Store) openPRs(ctx context.Context) ([]dinv.PR, error) {
	prs := s.prs.Load()
	if prs == nil {
		return nil, nil
	}
	return (*prs).List(ctx, true)
}

// New is the store of db, whose events reach the host's event log in db
// until UseHost gives it the broker.
// DB is the store the inventory keeps its tables in.
func (s *Store) DB() *sqlite.DB { return s.db }

func New(db *sqlite.DB) *Store {
	h := dbHost{sqlite.New(db)}
	return &Store{db: db, clock: time.Now, host: src.Host{Recorder: h, History: h}}
}

// exec runs one statement in its own write transaction and touches t for
// ref when it changed a row.
func (s *Store) exec(ctx context.Context, t port.Topic, ref github.Ref, query string, args ...any) error {
	_, err := s.db.Write(ctx, func(tx *sqlite.Tx) error {
		res, err := tx.ExecContext(ctx, query, args...)
		return touchChanged(tx, res, err, t, ref)
	})
	return err
}

// touchChanged touches t for ref when the statement that returned res and
// err changed a row.
func touchChanged(tx *sqlite.Tx, res sql.Result, err error, t port.Topic, ref github.Ref) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n > 0 {
		tx.Touch(t, ref.Unit())
	}
	return nil
}

// UseClock dates the events the store writes on its own, such as followed,
// by now.
func (s *Store) UseClock(now func() time.Time) { s.clock = now }

// Now is the store's clock.
func (s *Store) Now() time.Time { return s.clock() }

func ts(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(timeLayout)
}

func parseTS(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(timeLayout, s)
}

func joinList(v []string) string {
	if len(v) == 0 {
		return ""
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func splitList(s string) ([]string, error) {
	if s == "" {
		return nil, nil
	}
	var v []string
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil, err
	}
	return v, nil
}

// unitArgs are the provider and unit_key column values of ref; the zero ref
// is the zero unit.
func unitArgs(ref github.Ref) []any {
	u := ref.Unit()
	return []any{string(u.Provider), u.Key}
}

// keyValues is the placeholders and arguments of the unit keys of refs, for
// an IN on unit_key.
func keyValues(refs []github.Ref) (string, []any) {
	args := make([]any, 0, len(refs))
	for _, r := range refs {
		args = append(args, r.Unit().Key)
	}
	return strings.TrimSuffix(strings.Repeat("?,", len(refs)), ","), args
}

// refOf is the pull request of a unit's columns, the zero ref for a unit of
// another provider.
func refOf(provider, key string) github.Ref {
	if unit.ProviderID(provider) != unit.GitHub {
		return github.Ref{}
	}
	ref, _ := pr.ParseKey(key)
	return ref
}

// compareRefs orders refs by owner, repo and number.
func compareRefs(a, b github.Ref) int {
	return cmp.Or(cmp.Compare(a.Owner, b.Owner), cmp.Compare(a.Repo, b.Repo), cmp.Compare(a.Number, b.Number))
}

type scanner interface{ Scan(dest ...any) error }

// AppendEvents stores the events a source read, each at most once per pull
// request, kind and key, and returns how many were new.
func (s *Store) AppendEvents(ctx context.Context, evs []dinv.Event) (int, error) {
	return s.appendEvents(ctx, evs, port.TopicSourceEvents)
}

const insertEventSQL = `INSERT OR IGNORE INTO events (provider, unit_key, kind, key, actor, at, data) VALUES (?,?,?,?,?,?,?)`

// eventArgs are the insertEventSQL arguments of e.
func eventArgs(e dinv.Event) []any {
	u := sqlite.StoredUnit(e.PR.Unit(), e.Kind, e.Key)
	return []any{string(u.Provider), u.Key, e.Kind, e.Key, e.Actor, ts(e.At), e.Data}
}

// timed refuses events with no time.
func timed(evs []dinv.Event) error {
	for _, e := range evs {
		if e.At.IsZero() {
			return fmt.Errorf("event %s on %s/%s#%d has no time", e.Kind, e.PR.Owner, e.PR.Repo, e.PR.Number)
		}
	}
	return nil
}

func (s *Store) appendEvents(ctx context.Context, evs []dinv.Event, topic port.Topic) (added int, err error) {
	if err := timed(evs); err != nil {
		return 0, err
	}
	_, err = s.db.Write(ctx, func(tx *sqlite.Tx) error {
		added = 0
		stmt, err := tx.PrepareContext(ctx, insertEventSQL)
		if err != nil {
			return err
		}
		defer stmt.Close()
		byRef := map[github.Ref]int{}
		if err := addEvents(ctx, tx, stmt, evs, topic, byRef); err != nil {
			return err
		}
		for _, n := range byRef {
			added += n
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return added, nil
}

// addEvents inserts evs through insert, an insertEventSQL statement, counts
// the new ones per ref in added and touches topic for their refs.
func addEvents(ctx context.Context, tx *sqlite.Tx, insert *sql.Stmt, evs []dinv.Event, topic port.Topic, added map[github.Ref]int) error {
	for _, e := range evs {
		res, err := insert.ExecContext(ctx, eventArgs(e)...)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n > 0 {
			added[e.PR] += int(n)
			tx.Touch(topic, e.PR.Unit())
		}
	}
	return nil
}

// RecordEvent stores an event the loop writes. A keyed one is stored at
// most once per pull request, kind and key; an unkeyed one moves forward a
// nanosecond at a time while the same kind already sits at that time, since
// unkeyed events are unique by pull request, kind and time.
func (s *Store) RecordEvent(ctx context.Context, ev dinv.Event) error {
	if ev.Key != "" {
		_, err := s.appendEvents(ctx, []dinv.Event{ev}, port.TopicLoopEvents)
		return err
	}
	for range 1000 {
		added, err := s.appendEvents(ctx, []dinv.Event{ev}, port.TopicLoopEvents)
		if err != nil || added > 0 {
			return err
		}
		ev.At = ev.At.Add(time.Nanosecond)
	}
	return errors.New("recording " + ev.Kind + ": no free timestamp")
}

func (s *Store) Events(ctx context.Context, ref github.Ref) ([]dinv.Event, error) {
	byPR, err := s.EventsByPR(ctx, []github.Ref{ref})
	if err != nil {
		return nil, err
	}
	return byPR[ref], nil
}

const eventsByUnitsSQL = `SELECT ` + eventColumns + ` FROM events
	WHERE (provider, unit_key) IN (SELECT value->>0, value->>1 FROM json_each(?))
	ORDER BY at, id`

// EventsByPR reads the events of refs, oldest first per pull request, in
// one statement whatever the number of refs.
func (s *Store) EventsByPR(ctx context.Context, refs []github.Ref) (map[github.Ref][]dinv.Event, error) {
	out := make(map[github.Ref][]dinv.Event, len(refs))
	if len(refs) == 0 {
		return out, nil
	}
	keys := make([][2]string, len(refs))
	for i, r := range refs {
		u := r.Unit()
		keys[i] = [2]string{string(u.Provider), u.Key}
	}
	raw, err := json.Marshal(keys)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.R().QueryContext(ctx, eventsByUnitsSQL, string(raw))
	if err != nil {
		return nil, err
	}
	evs, err := scanEvents(rows)
	if err != nil {
		return nil, err
	}
	for _, e := range evs {
		out[e.PR] = append(out[e.PR], e)
	}
	return out, nil
}

// UnitlessEvents reads the events of no pull request, those stored under
// the zero unit or a session unit, oldest first.
func (s *Store) UnitlessEvents(ctx context.Context) ([]dinv.Event, error) {
	rows, err := s.db.R().QueryContext(ctx, `SELECT `+eventColumns+` FROM events WHERE provider IN ('', ?) ORDER BY at, id`, string(unit.Session))
	if err != nil {
		return nil, err
	}
	return scanEvents(rows)
}

func endedSQL(marks string) string {
	return `SELECT DISTINCT unit_key FROM events WHERE provider = 'github' AND unit_key IN (` + marks + `) AND kind=? AND key != ''`
}

// Ended says, per pull request in refs, whether a keyed event of kind exists.
func (s *Store) Ended(ctx context.Context, kind string, refs []github.Ref) (map[github.Ref]bool, error) {
	out := make(map[github.Ref]bool, len(refs))
	for chunk := range slices.Chunk(refs, eventsChunk) {
		marks, args := keyValues(chunk)
		rows, err := s.db.R().QueryContext(ctx, endedSQL(marks), append(args, kind)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var key string
			if err := rows.Scan(&key); err != nil {
				rows.Close()
				return nil, err
			}
			out[refOf(string(unit.GitHub), key)] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

const lastEventSQL = `SELECT ` + eventColumns + ` FROM events WHERE provider = ? AND unit_key = ? AND kind = ?
	ORDER BY at DESC, id DESC LIMIT 1`

// LastEvent is the newest event of kind on ref.
func (s *Store) LastEvent(ctx context.Context, ref github.Ref, kind string) (dinv.Event, bool, error) {
	rows, err := s.db.R().QueryContext(ctx, lastEventSQL, append(unitArgs(ref), kind)...)
	if err != nil {
		return dinv.Event{}, false, err
	}
	evs, err := scanEvents(rows)
	if err != nil || len(evs) == 0 {
		return dinv.Event{}, false, err
	}
	return evs[0], true, nil
}

// FirstRoundBlocks maps each unit whose round is blocked to its block, ended
// as dinv.ActiveBlock ends it, the rule a pull request's row shows.
func (s *Store) FirstRoundBlocks(ctx context.Context) (map[unit.Unit]port.Event, error) {
	rows, err := s.db.R().QueryContext(ctx, `SELECT `+eventColumns+` FROM events WHERE kind IN (?, ?) ORDER BY at, id`, dinv.KindRoundBlocked, dinv.KindRoundUnblocked)
	if err != nil {
		return nil, err
	}
	evs, err := scanUnitEvents(rows)
	if err != nil {
		return nil, err
	}
	byUnit := map[unit.Unit][]dinv.Event{}
	for _, e := range evs {
		byUnit[e.unit] = append(byUnit[e.unit], e.Event)
	}
	var since time.Time
	for u, evs := range byUnit {
		b, ok := dinv.ActiveBlock(evs, "")
		if !ok {
			delete(byUnit, u)
			continue
		}
		if since.IsZero() || b.At.Before(since) {
			since = b.At
		}
	}
	if len(byUnit) == 0 {
		return map[unit.Unit]port.Event{}, nil
	}
	viewer, err := s.Viewer(ctx, unit.GitHub)
	if err != nil {
		return nil, err
	}
	rows, err = s.db.R().QueryContext(ctx, `SELECT `+eventColumns+` FROM events WHERE kind IN (?, ?, ?) AND at >= ? ORDER BY at, id`,
		dinv.KindRoundSent, dinv.KindRoundHeld, dinv.KindReview, ts(since))
	if err != nil {
		return nil, err
	}
	later, err := scanUnitEvents(rows)
	if err != nil {
		return nil, err
	}
	for _, e := range later {
		if evs, ok := byUnit[e.unit]; ok {
			byUnit[e.unit] = append(evs, e.Event)
		}
	}
	out := map[unit.Unit]port.Event{}
	for u, evs := range byUnit {
		slices.SortStableFunc(evs, func(a, b dinv.Event) int { return a.At.Compare(b.At) })
		if b, ok := dinv.ActiveBlock(evs, viewer); ok {
			out[u] = port.Event{Unit: u, Kind: b.Kind, Key: b.Key, Data: b.Data, At: b.At}
		}
	}
	return out, nil
}

// RecordUnitEvent stores an event the loop writes on any unit, as
// RecordEvent stores one on a pull request.
func (s *Store) RecordUnitEvent(ctx context.Context, ev port.Event) error {
	return sqlite.New(s.db).RecordEvents(ctx, ev)
}

// unitEvent is a stored event with the unit it is stored under.
type unitEvent struct {
	dinv.Event
	unit unit.Unit
}

func scanUnitEvents(rows *sql.Rows) ([]unitEvent, error) {
	defer rows.Close()
	var out []unitEvent
	for rows.Next() {
		var e unitEvent
		var provider, at string
		if err := rows.Scan(&provider, &e.unit.Key, &e.Kind, &e.Key, &e.Actor, &at, &e.Data); err != nil {
			return nil, err
		}
		e.unit.Provider = unit.ProviderID(provider)
		e.PR = refOf(provider, e.unit.Key)
		var err error
		if e.At, err = parseTS(at); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

const eventColumns = `provider, unit_key, kind, key, actor, at, data`

// scanEvents reads every row and closes rows.
func scanEvents(rows *sql.Rows) ([]dinv.Event, error) {
	defer rows.Close()
	var out []dinv.Event
	for rows.Next() {
		var e dinv.Event
		var provider, key, at string
		if err := rows.Scan(&provider, &key, &e.Kind, &e.Key, &e.Actor, &at, &e.Data); err != nil {
			return nil, err
		}
		e.PR = refOf(provider, key)
		var err error
		if e.At, err = parseTS(at); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

const (
	overrideWriteColumns = `provider, unit_key, muted_until, mute_until_state, hold, taken_over, muted_kind, muted_waiting_on, tracked`
	overrideColumns      = overrideWriteColumns + `, next_guidance`
)

func scanOverride(row scanner) (dinv.Override, error) {
	var o dinv.Override
	var provider, key, muted string
	if err := row.Scan(&provider, &key, &muted, &o.MuteUntilStateChange, &o.Hold, &o.Adopted, &o.MutedKind, &o.MutedWaitingOn,
		&o.Tracked, &o.NextGuidance); err != nil {
		return dinv.Override{}, err
	}
	o.PR = refOf(provider, key)
	var err error
	if o.MutedUntil, err = parseTS(muted); err != nil {
		return dinv.Override{}, err
	}
	return o, nil
}

// SetNextGuidance keeps guidance that came after its round, for the next one.
func (s *Store) SetNextGuidance(ctx context.Context, ref github.Ref, guidance string) error {
	return s.exec(ctx, port.TopicOverrides, ref, `INSERT INTO overrides (provider, unit_key, next_guidance) VALUES (?,?,?)
	 ON CONFLICT(provider, unit_key) DO UPDATE SET next_guidance=excluded.next_guidance WHERE next_guidance IS NOT excluded.next_guidance`,
		append(unitArgs(ref), guidance)...)
}

// KeepNextGuidance keeps the guidance of a round that was not sent, or whose
// prompt was not seen landing, for the next round. Guidance given since wins.
func (s *Store) KeepNextGuidance(ctx context.Context, ref github.Ref, guidance string) error {
	return s.exec(ctx, port.TopicOverrides, ref, `INSERT INTO overrides (provider, unit_key, next_guidance) VALUES (?,?,?)
	 ON CONFLICT(provider, unit_key) DO UPDATE SET next_guidance=excluded.next_guidance WHERE next_guidance='' AND excluded.next_guidance IS NOT ''`,
		append(unitArgs(ref), guidance)...)
}

// TakeNextGuidance returns the kept guidance and clears it.
func (s *Store) TakeNextGuidance(ctx context.Context, ref github.Ref) (string, error) {
	var guidance string
	_, err := s.db.Write(ctx, func(tx *sqlite.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT next_guidance FROM overrides WHERE provider=? AND unit_key=?`,
			unitArgs(ref)...).Scan(&guidance)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && guidance == "") {
			return nil
		}
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE overrides SET next_guidance='' WHERE provider=? AND unit_key=?`,
			unitArgs(ref)...)
		return touchChanged(tx, res, err, port.TopicOverrides, ref)
	})
	if err != nil {
		return "", err
	}
	return guidance, nil
}

func (s *Store) Override(ctx context.Context, ref github.Ref) (dinv.Override, error) {
	o, err := scanOverride(s.db.R().QueryRowContext(ctx, `SELECT `+overrideColumns+` FROM overrides WHERE provider=? AND unit_key=?`, unitArgs(ref)...))
	if errors.Is(err, sql.ErrNoRows) {
		return dinv.Override{PR: ref}, nil
	}
	return o, err
}

func (s *Store) Overrides(ctx context.Context) (map[github.Ref]dinv.Override, error) {
	rows, err := s.db.R().QueryContext(ctx, `SELECT `+overrideColumns+` FROM overrides WHERE provider = 'github'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[github.Ref]dinv.Override{}
	for rows.Next() {
		o, err := scanOverride(rows)
		if err != nil {
			return nil, err
		}
		out[o.PR] = o
	}
	return out, rows.Err()
}

func (s *Store) Meta(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.R().QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// MetaPrefixed is every meta value whose key starts with prefix, by key.
func (s *Store) MetaPrefixed(ctx context.Context, prefix string) (map[string]string, error) {
	rows, err := s.db.R().QueryContext(ctx, `SELECT key, value FROM meta WHERE substr(key, 1, length(?)) = ?`, prefix, prefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

func (s *Store) SetMeta(ctx context.Context, key, value string) error {
	_, err := s.db.Write(ctx, func(tx *sqlite.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES (?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value WHERE value IS NOT excluded.value`, key, value)
		return touchChanged(tx, res, err, port.TopicMeta, github.Ref{})
	})
	return err
}

// SetMetaNoting is SetMeta that also notes the extra topics in the same
// transaction.
func (s *Store) SetMetaNoting(ctx context.Context, key, value string, extra port.Topic) error {
	_, err := s.db.Write(ctx, func(tx *sqlite.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO meta (key, value) VALUES (?, ?)`, key, value); err != nil {
			return err
		}
		tx.Touch(port.TopicMeta | extra)
		return nil
	})
	return err
}

// SetMetas writes every key of kv in one transaction and notes Meta once
// when any value changed.
func (s *Store) SetMetas(ctx context.Context, kv map[string]string) error {
	if len(kv) == 0 {
		return nil
	}
	_, err := s.db.Write(ctx, func(tx *sqlite.Tx) error {
		changed := false
		for k, v := range kv {
			res, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES (?, ?)
			 ON CONFLICT(key) DO UPDATE SET value=excluded.value WHERE value IS NOT excluded.value`, k, v)
			if err != nil {
				return err
			}
			if n, err := res.RowsAffected(); err != nil {
				return err
			} else if n > 0 {
				changed = true
			}
		}
		if changed {
			tx.Touch(port.TopicMeta)
		}
		return nil
	})
	return err
}

const viewerKeyPrefix = "viewer:"

// SetViewer stores the provider's viewer login, drops the unprefixed row an
// earlier schema kept, and notes TopicViewer when the login changed.
func (s *Store) SetViewer(ctx context.Context, provider unit.ProviderID, login string) error {
	key := viewerKeyPrefix + string(provider)
	_, err := s.db.Write(ctx, func(tx *sqlite.Tx) error {
		var old string
		err := tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, key).Scan(&old)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM meta WHERE key='viewer'`); err != nil {
			return err
		}
		if err == nil && old == login {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO meta (key, value) VALUES (?, ?)`, key, login); err != nil {
			return err
		}
		tx.Touch(port.TopicViewer)
		return nil
	})
	return err
}

// Viewer is the provider's stored viewer login, "" before it is known.
func (s *Store) Viewer(ctx context.Context, provider unit.ProviderID) (string, error) {
	return s.Meta(ctx, viewerKeyPrefix+string(provider))
}

// Viewers is every stored viewer login by provider.
func (s *Store) Viewers(ctx context.Context) (map[unit.ProviderID]string, error) {
	raw, err := s.MetaPrefixed(ctx, viewerKeyPrefix)
	if err != nil {
		return nil, err
	}
	out := map[unit.ProviderID]string{}
	for k, v := range raw {
		out[unit.ProviderID(strings.TrimPrefix(k, viewerKeyPrefix))] = v
	}
	return out, nil
}

// SaveTurn keeps the text a turn was sent with, its guide sources and the
// hash of the bundled guide of its kind at the time. The text is stored as
// deflated sections shared between turns.
func (s *Store) SaveTurn(ctx context.Context, t dinv.StoredTurn, now time.Time) error {
	parts := splitParts(t.Text)
	shas := make([]string, len(parts))
	for i, p := range parts {
		shas[i] = partSHA(p)
	}
	list, err := json.Marshal(shas)
	if err != nil {
		return err
	}
	_, err = s.db.Write(ctx, func(tx *sqlite.Tx) error {
		have, err := storedParts(ctx, tx, string(list))
		if err != nil {
			return err
		}
		for i, p := range parts {
			if have[shas[i]] {
				continue
			}
			body, err := deflatePart(p)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO instruction_parts (sha, body) VALUES (?, ?)`, shas[i], body); err != nil {
				return err
			}
			have[shas[i]] = true
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO turn_instructions (sha, kind, repo, parts, created_at, bundled_sha, sources) VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(sha) DO UPDATE SET parts=excluded.parts, created_at=excluded.created_at, bundled_sha=excluded.bundled_sha, sources=excluded.sources`,
			t.ID, t.Kind, t.Repo, string(list), now.UTC().Format(timeLayout), t.BundledSHA, strings.Join(t.Sources, "\n")); err != nil {
			return err
		}
		tx.Touch(port.TopicRounds)
		return nil
	})
	return err
}

func storedParts(ctx context.Context, tx *sqlite.Tx, list string) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT sha FROM instruction_parts WHERE sha IN (SELECT value FROM json_each(?))`, list)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	have := map[string]bool{}
	for rows.Next() {
		var sha string
		if err := rows.Scan(&sha); err != nil {
			return nil, err
		}
		have[sha] = true
	}
	return have, rows.Err()
}

// Turn is the turn kept under id; ok is false once it is gone or never was.
func (s *Store) Turn(ctx context.Context, id string) (dinv.StoredTurn, bool, error) {
	t := dinv.StoredTurn{ID: id}
	found := false
	err := s.db.Read(ctx, func(tx *sql.Tx) error {
		var parts, sources string
		err := tx.QueryRowContext(ctx, `SELECT kind, repo, parts, bundled_sha, sources FROM turn_instructions WHERE sha = ?`, id).
			Scan(&t.Kind, &t.Repo, &parts, &t.BundledSHA, &sources)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if t.Text, err = readParts(ctx, tx, parts); err != nil {
			return err
		}
		if sources != "" {
			t.Sources = strings.Split(sources, "\n")
		}
		found = true
		return nil
	})
	if err != nil || !found {
		return dinv.StoredTurn{}, false, err
	}
	return t, true, nil
}

const latestBundleSQL = `SELECT bundled_sha FROM turn_instructions WHERE kind = ? AND repo = ? ORDER BY created_at DESC LIMIT 1`

// LastBundledSHA is the bundled guide hash of the latest round of kind in
// repo; ok is false before any.
func (s *Store) LastBundledSHA(ctx context.Context, kind, repo string) (string, bool, error) {
	var sha string
	err := s.db.R().QueryRowContext(ctx, latestBundleSQL, kind, repo).Scan(&sha)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return sha, err == nil, err
}
