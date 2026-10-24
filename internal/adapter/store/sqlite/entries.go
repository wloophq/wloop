package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/event"
	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type entryStore struct{ db *DB }

var _ coregtd.Store = entryStore{}

func (db *DB) Entries() coregtd.Store { return entryStore{db} }

const entryColumns = `id, kind, state, title, notes, focus_on, focus_auto, focus_at, area, project, position, sequential,
	snooze_until, snooze_on_activity, snooze_return, snooze_mute, waiting, deadline, changed, seen_at, origin, created, updated`

const (
	notEnded     = `state NOT IN ('done', 'trash')`
	entryOrder   = ` ORDER BY created, id`
	changedSince = `rev > ?`
	ofUnit       = `id IN (SELECT entry_id FROM entry_units WHERE provider = ? AND unit_key = ?
		UNION SELECT entry_id FROM entry_items WHERE provider = ? AND unit_key = ?)`
)

// candidates is the SQL condition on entries that holds every entry the view predicate of v may select,
// and what the sequential rule of Next needs.
func candidates(v gtd.View) string {
	switch v {
	case gtd.ViewFocus:
		return `focus_on = 1 AND ` + notEnded
	case gtd.ViewInbox, gtd.ViewWaiting, gtd.ViewSomeday:
		return `state = '` + string(v) + `'`
	case gtd.ViewNext:
		return `state = 'next' OR (project != '' AND state NOT IN ('done', 'trash', 'reference')) OR (kind = 'project' AND sequential = 1)`
	case gtd.ViewProjects:
		return `kind = 'project' AND ` + notEnded
	case gtd.ViewLogbook:
		return `state IN ('reference', 'done')`
	case gtd.ViewOpen:
		return notEnded
	}
	return `0`
}

func entryTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(event.TimeLayout, s)
}

func scanEntry(row scanner) (gtd.Entry, error) {
	var e gtd.Entry
	var focusAt, snoozeUntil, waiting, deadline, seenAt, created, updated string
	var kind, state, project, ret string
	if err := row.Scan(&e.ID, &kind, &state, &e.Title, &e.Notes, &e.Focus.On, &e.Focus.Auto, &focusAt, &e.Area, &project,
		&e.Position, &e.Sequential, &snoozeUntil, &e.Snooze.OnActivity, &ret, &e.Snooze.Mute, &waiting, &deadline,
		&e.Changed, &seenAt, &e.Origin, &created, &updated); err != nil {
		return gtd.Entry{}, err
	}
	e.Kind, e.State, e.Project, e.Snooze.Return = gtd.Kind(kind), gtd.State(state), gtd.ID(project), gtd.State(ret)
	for _, f := range []struct {
		dst *time.Time
		raw string
	}{{&e.Focus.At, focusAt}, {&e.Snooze.Until, snoozeUntil}, {&e.Deadline, deadline}, {&e.SeenAt, seenAt}, {&e.Created, created}, {&e.Updated, updated}} {
		t, err := entryTime(f.raw)
		if err != nil {
			return gtd.Entry{}, fmt.Errorf("a time of entry %s: %w", e.ID, err)
		}
		*f.dst = t
	}
	if waiting != "" {
		e.Waiting = &gtd.WaitingOn{}
		if err := json.Unmarshal([]byte(waiting), e.Waiting); err != nil {
			return gtd.Entry{}, fmt.Errorf("the waiting of entry %s: %w", e.ID, err)
		}
	}
	return e, nil
}

// loadEntries reads the entries matching where, with their items, units and contexts, oldest first.
func loadEntries(ctx context.Context, q querier, where string, args ...any) ([]gtd.Entry, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+entryColumns+` FROM entries WHERE `+where+entryOrder, args...)
	if err != nil {
		return nil, err
	}
	var out []gtd.Entry
	at := map[gtd.ID]int{}
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		at[e.ID] = len(out)
		out = append(out, e)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil || len(out) == 0 {
		return out, err
	}
	sub := `(SELECT id FROM entries WHERE ` + where + `)`
	err = eachRow(ctx, q, `SELECT entry_id, provider, unit_key, item FROM entry_items WHERE entry_id IN `+sub+
		` ORDER BY entry_id, provider, unit_key, item`, args, func(r *sql.Rows) error {
		var id string
		var ref gtd.ItemRef
		if err := r.Scan(&id, &ref.Unit.Provider, &ref.Unit.Key, &ref.Item); err != nil {
			return err
		}
		e := &out[at[gtd.ID(id)]]
		e.Items = append(e.Items, ref)
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = eachRow(ctx, q, `SELECT entry_id, provider, unit_key FROM entry_units WHERE entry_id IN `+sub+` ORDER BY entry_id, ord`,
		args, func(r *sql.Rows) error {
			var id string
			var u unit.Unit
			if err := r.Scan(&id, &u.Provider, &u.Key); err != nil {
				return err
			}
			e := &out[at[gtd.ID(id)]]
			e.Units = append(e.Units, u)
			return nil
		})
	if err != nil {
		return nil, err
	}
	err = eachRow(ctx, q, `SELECT entry_id, context FROM entry_contexts WHERE entry_id IN `+sub+` ORDER BY entry_id, ord`,
		args, func(r *sql.Rows) error {
			var id, c string
			if err := r.Scan(&id, &c); err != nil {
				return err
			}
			e := &out[at[gtd.ID(id)]]
			e.Contexts = append(e.Contexts, c)
			return nil
		})
	if err != nil {
		return nil, err
	}
	return out, loadProjects(ctx, q, sub, args, out, at)
}

func eachRow(ctx context.Context, q querier, query string, args []any, fn func(*sql.Rows) error) error {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

func readPosition(ctx context.Context, q querier) (epoch string, rev uint64, err error) {
	rows, err := q.QueryContext(ctx, `SELECT epoch, rev FROM entry_position WHERE only = 1`)
	if err != nil {
		return "", 0, err
	}
	defer rows.Close()
	if rows.Next() {
		err = rows.Scan(&epoch, &rev)
	}
	return epoch, rev, errors.Join(err, rows.Err())
}

func (s entryStore) Entries(ctx context.Context, q gtd.Query) ([]gtd.Entry, error) {
	var out []gtd.Entry
	err := s.db.Read(ctx, func(tx *sql.Tx) error {
		all, err := loadEntries(ctx, tx, candidates(q.View))
		out = gtd.Select(all, q)
		return err
	})
	return out, err
}

func (s entryStore) EntriesSince(ctx context.Context, q gtd.Query, since coregtd.Since) (coregtd.Page, error) {
	var p coregtd.Page
	err := s.db.Read(ctx, func(tx *sql.Tx) error {
		var err error
		if p.Epoch, p.Rev, err = readPosition(ctx, tx); err != nil {
			return err
		}
		if p.Counters, err = countsTx(ctx, tx, q.Area); err != nil {
			return err
		}
		if since.Epoch != p.Epoch || since.Rev > p.Rev {
			p.Full = true
			all, err := loadEntries(ctx, tx, candidates(q.View))
			p.Entries = gtd.Select(all, q)
			return err
		}
		all, err := loadEntries(ctx, tx, candidates(q.View))
		if err != nil {
			return err
		}
		changed, err := loadEntries(ctx, tx, changedSince, since.Rev)
		if err != nil {
			return err
		}
		held := map[gtd.ID]bool{}
		for _, e := range all {
			held[e.ID] = true
		}
		revs := map[gtd.ID]bool{}
		for _, e := range changed {
			revs[e.ID] = true
			if !held[e.ID] {
				all = append(all, e)
			}
		}
		p.Entries, p.Removed = coregtd.Delta(all, q, func(id gtd.ID) bool { return revs[id] })
		return nil
	})
	return p, err
}

func (s entryStore) Entry(ctx context.Context, id gtd.ID) (gtd.Entry, error) {
	es, err := loadEntries(ctx, s.db.R(), `id = ?`, string(id))
	if err != nil {
		return gtd.Entry{}, err
	}
	if len(es) == 0 {
		return gtd.Entry{}, fmt.Errorf("%w: %s", coregtd.ErrNoEntry, id)
	}
	return es[0], nil
}

func (s entryStore) EntriesOf(ctx context.Context, u unit.Unit) ([]gtd.Entry, error) {
	var out []gtd.Entry
	err := s.db.Read(ctx, func(tx *sql.Tx) error {
		var err error
		out, err = loadEntries(ctx, tx, ofUnit, string(u.Provider), u.Key, string(u.Provider), u.Key)
		return err
	})
	return out, err
}

func (s entryStore) Counts(ctx context.Context, area string) (gtd.Counters, error) {
	var c gtd.Counters
	err := s.db.Read(ctx, func(tx *sql.Tx) error {
		var err error
		c, err = countsTx(ctx, tx, area)
		return err
	})
	return c, err
}

// nextIDs selects the entries Next lists: State next, and of a sequential project only its open action with the
// lowest Position (then id), as gtd.Select does over the candidates of ViewNext.
const nextIDs = `WITH seq AS (SELECT id FROM entries WHERE kind = 'project' AND sequential = 1),
first AS (SELECT id FROM (SELECT id, ROW_NUMBER() OVER (PARTITION BY project ORDER BY position, id) AS n
	FROM entries WHERE project IN seq AND state NOT IN ('done', 'trash', 'reference')) WHERE n = 1),
nxt AS (SELECT id, area FROM entries WHERE state = 'next' AND (project NOT IN seq OR id IN first)) `

const inArea = `(? = '' OR area = ?)`

// countsTx is gtd.CountsOf over the stored views, one query per map.
func countsTx(ctx context.Context, tx *sql.Tx, area string) (gtd.Counters, error) {
	c := gtd.Counters{Area: area, Views: map[gtd.View]int{gtd.ViewOutbox: 0}, Areas: map[string]int{}, Contexts: map[string]int{}}
	var focus, inbox, next, waiting, projects, someday, logbook int
	if err := tx.QueryRowContext(ctx, nextIDs+`SELECT
		COALESCE(SUM(focus_on = 1 AND `+notEnded+` AND `+inArea+`), 0),
		COALESCE(SUM(state = 'inbox'), 0),
		(SELECT COUNT(*) FROM nxt WHERE `+inArea+`),
		COALESCE(SUM(state = 'waiting' AND `+inArea+`), 0),
		COALESCE(SUM(kind = 'project' AND `+notEnded+` AND `+inArea+`), 0),
		COALESCE(SUM(state = 'someday' AND `+inArea+`), 0),
		COALESCE(SUM(state IN ('reference', 'done') AND `+inArea+`), 0)
		FROM entries`, area, area, area, area, area, area, area, area, area, area, area, area).
		Scan(&focus, &inbox, &next, &waiting, &projects, &someday, &logbook); err != nil {
		return gtd.Counters{}, err
	}
	for v, n := range map[gtd.View]int{gtd.ViewFocus: focus, gtd.ViewInbox: inbox, gtd.ViewNext: next, gtd.ViewWaiting: waiting,
		gtd.ViewProjects: projects, gtd.ViewSomeday: someday, gtd.ViewLogbook: logbook} {
		c.Views[v] = n
	}
	err := eachRow(ctx, tx, nextIDs+`SELECT area, COUNT(*) FROM nxt WHERE area != '' GROUP BY area`, nil, func(r *sql.Rows) error {
		var a string
		var n int
		if err := r.Scan(&a, &n); err != nil {
			return err
		}
		c.Areas[a] = n
		return nil
	})
	if err != nil {
		return gtd.Counters{}, err
	}
	err = eachRow(ctx, tx, nextIDs+`SELECT x.context, COUNT(*) FROM nxt JOIN entry_contexts x ON x.entry_id = nxt.id
		WHERE `+inArea+` GROUP BY x.context`, []any{area, area}, func(r *sql.Rows) error {
		var cx string
		var n int
		if err := r.Scan(&cx, &n); err != nil {
			return err
		}
		c.Contexts[cx] = n
		return nil
	})
	if err != nil {
		return gtd.Counters{}, err
	}
	c.Badge = c.Views[gtd.ViewInbox] + c.Views[gtd.ViewOutbox]
	return c, nil
}

func newEpoch() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (s entryStore) Apply(ctx context.Context, cs []gtd.Change, evs ...port.Event) error {
	for _, ev := range evs {
		if ev.At.IsZero() {
			return fmt.Errorf("the %s event has no time", ev.Kind)
		}
	}
	_, err := s.db.Write(ctx, func(tx *Tx) error {
		changed := map[gtd.ID]gtd.Entry{}
		var order []gtd.ID
		for _, c := range cs {
			e := c.Entry
			if e.ID == "" {
				return errors.New("an entry without an id")
			}
			old, err := loadEntries(ctx, tx, `id = ?`, string(e.ID))
			if err != nil {
				return err
			}
			if len(old) == 1 && old[0].Same(e) {
				delete(changed, e.ID)
				continue
			}
			if _, ok := changed[e.ID]; !ok {
				order = append(order, e.ID)
			}
			changed[e.ID] = e
		}
		if len(changed) == 0 {
			return nil
		}
		epoch, rev, err := readPosition(ctx, tx)
		if err != nil {
			return err
		}
		if epoch == "" {
			epoch = newEpoch()
		}
		rev++
		if _, err := tx.ExecContext(ctx, `INSERT INTO entry_position (only, epoch, rev) VALUES (1, ?, ?)
			ON CONFLICT (only) DO UPDATE SET epoch = excluded.epoch, rev = excluded.rev`, epoch, rev); err != nil {
			return err
		}
		for _, id := range order {
			e, ok := changed[id]
			if !ok {
				continue
			}
			if err := writeEntry(ctx, tx, e, rev); err != nil {
				return err
			}
			tx.Touch(port.TopicEntries, entryUnits(e)...)
		}
		return writeEvents(ctx, tx, evs)
	})
	return err
}

func entryUnits(e gtd.Entry) []unit.Unit {
	out := append([]unit.Unit(nil), e.Units...)
	for _, r := range e.Items {
		out = append(out, r.Unit)
	}
	return out
}

func writeEntry(ctx context.Context, tx *Tx, e gtd.Entry, rev uint64) error {
	waiting := ""
	if e.Waiting != nil {
		b, err := json.Marshal(e.Waiting)
		if err != nil {
			return err
		}
		waiting = string(b)
	}
	for _, table := range []string{"entry_items", "entry_units", "entry_contexts"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE entry_id = ?`, string(e.ID)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO entries (`+entryColumns+`, rev)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(e.ID), string(e.Kind), string(e.State), e.Title, e.Notes, e.Focus.On, e.Focus.Auto, eventTime(e.Focus.At),
		e.Area, string(e.Project), e.Position, e.Sequential, eventTime(e.Snooze.Until), e.Snooze.OnActivity,
		string(e.Snooze.Return), e.Snooze.Mute, waiting, eventTime(e.Deadline), e.Changed, eventTime(e.SeenAt), e.Origin,
		eventTime(e.Created), eventTime(e.Updated), rev); err != nil {
		return err
	}
	for _, r := range e.Items {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO entry_items (entry_id, provider, unit_key, item) VALUES (?, ?, ?, ?)`,
			string(e.ID), string(r.Unit.Provider), r.Unit.Key, r.Item); err != nil {
			return err
		}
	}
	for i, u := range e.Units {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO entry_units (entry_id, provider, unit_key, ord) VALUES (?, ?, ?, ?)`,
			string(e.ID), string(u.Provider), u.Key, i); err != nil {
			return err
		}
	}
	for i, c := range e.Contexts {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO entry_contexts (entry_id, context, ord) VALUES (?, ?, ?)`,
			string(e.ID), c, i); err != nil {
			return err
		}
	}
	return writeProject(ctx, tx, e)
}
