package sqlite

import (
	"cmp"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/event"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type taskStore struct{ db *DB }

var _ coretasks.Store = taskStore{}

func (db *DB) Tasks() coretasks.Store { return taskStore{db} }

const taskFields = `id, kind, state, category, title, notes, parent, position, sequential,
	snooze_until, snooze_on_activity, snooze_return, snooze_mute, waiting, changed, seen_at, origin, created, updated,
	review_every, reviewed_at`

// upsertTaskSQL updates an existing task in place: a REPLACE would delete
// the row first and cascade into its projects row.
var upsertTaskSQL = func() string {
	cols := strings.Split(taskFields+`, rev`, ",")
	set := make([]string, 0, len(cols)-1)
	for _, c := range cols[1:] {
		c = strings.TrimSpace(c)
		set = append(set, c+` = excluded.`+c)
	}
	return `INSERT INTO tasks (` + taskFields + `, rev) VALUES (?` + strings.Repeat(`, ?`, len(cols)-1) +
		`) ON CONFLICT (id) DO UPDATE SET ` + strings.Join(set, `, `)
}()

// The SQL conditions on tasks bind every category as a parameter.
const (
	taskOrder    = ` ORDER BY created, id`
	changedSince = `rev > ?`
	ofUnit       = `id IN (SELECT task_id FROM task_units WHERE provider = ? AND unit_key = ?
		UNION SELECT task_id FROM task_items WHERE provider = ? AND unit_key = ?)`
	isOpen    = `category NOT IN (?, ?)`
	isSomeday = `category = ? AND snooze_until = '' AND snooze_on_activity = 0 AND snooze_mute = 0`
	isStarred = `id IN (SELECT task_id FROM task_props WHERE name = ? AND json_extract(value, '$.text') = ?)`
)

func openArgs() []any { return []any{string(task.CategoryDone), string(task.CategoryDropped)} }

// candidates is the SQL condition on tasks, with its arguments, that holds every task the view predicate of v may
// select, and what the sequential rule of Next needs.
func candidates(v task.View) (string, []any) {
	switch v {
	case task.ViewFocus:
		return isStarred + ` AND ` + isOpen, append([]any{task.PropFocus, task.FocusOn}, openArgs()...)
	case task.ViewInbox:
		return `category = ?`, []any{string(task.CategoryInbox)}
	case task.ViewWaiting:
		return `category = ?`, []any{string(task.CategoryWaiting)}
	case task.ViewSomeday:
		return isSomeday, []any{string(task.CategoryDeferred)}
	case task.ViewNext:
		return `category = ? OR (parent != '' AND ` + isOpen + `) OR (kind = 'project' AND sequential = 1)`,
			append([]any{string(task.CategoryReady)}, openArgs()...)
	case task.ViewProjects:
		return `kind = 'project' AND ` + isOpen, openArgs()
	case task.ViewLogbook:
		return `category = ?`, []any{string(task.CategoryDone)}
	case task.ViewOpen:
		return isOpen, openArgs()
	}
	return `0`, nil
}

func taskTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(event.TimeLayout, s)
}

// propTime reads a property's write time, which PropStore and Apply stamp in their layouts.
func propTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, s)
}

func scanTask(row scanner) (task.Task, error) {
	var e task.Task
	var snoozeUntil, waiting, seenAt, created, updated, reviewed string
	var kind, state, category, parent, ret string
	var every int64
	if err := row.Scan(&e.ID, &kind, &state, &category, &e.Title, &e.Notes, &parent,
		&e.Position, &e.Sequential, &snoozeUntil, &e.Snooze.OnActivity, &ret, &e.Snooze.Mute, &waiting,
		&e.Changed, &seenAt, &e.Origin, &created, &updated, &every, &reviewed); err != nil {
		return task.Task{}, err
	}
	e.Kind, e.State, e.Category, e.Parent, e.Snooze.Return = task.Kind(kind), task.State(state), task.Category(category), task.ID(parent), task.State(ret)
	e.ReviewEvery = time.Duration(every) * time.Second
	for _, f := range []struct {
		dst *time.Time
		raw string
	}{{&e.Snooze.Until, snoozeUntil}, {&e.SeenAt, seenAt}, {&e.Created, created}, {&e.Updated, updated}, {&e.ReviewedAt, reviewed}} {
		t, err := taskTime(f.raw)
		if err != nil {
			return task.Task{}, fmt.Errorf("a time of task %s: %w", e.ID, err)
		}
		*f.dst = t
	}
	if waiting != "" {
		e.Waiting = &task.WaitingOn{}
		if err := json.Unmarshal([]byte(waiting), e.Waiting); err != nil {
			return task.Task{}, fmt.Errorf("the waiting of task %s: %w", e.ID, err)
		}
	}
	return e, nil
}

// loadTasks reads the tasks matching where, with their items, units and properties, oldest first.
func loadTasks(ctx context.Context, q querier, where string, args ...any) ([]task.Task, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+taskFields+` FROM tasks WHERE `+where+taskOrder, args...)
	if err != nil {
		return nil, err
	}
	var out []task.Task
	at := map[task.ID]int{}
	for rows.Next() {
		e, err := scanTask(rows)
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
	sub := `(SELECT id FROM tasks WHERE ` + where + `)`
	err = eachRow(ctx, q, `SELECT task_id, provider, unit_key, item FROM task_items WHERE task_id IN `+sub+
		` ORDER BY task_id, provider, unit_key, item`, args, func(r *sql.Rows) error {
		var id string
		var ref task.ItemRef
		if err := r.Scan(&id, &ref.Unit.Provider, &ref.Unit.Key, &ref.Item); err != nil {
			return err
		}
		e := &out[at[task.ID(id)]]
		e.Items = append(e.Items, ref)
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = eachRow(ctx, q, `SELECT task_id, provider, unit_key FROM task_units WHERE task_id IN `+sub+` ORDER BY task_id, ord`,
		args, func(r *sql.Rows) error {
			var id string
			var u unit.Unit
			if err := r.Scan(&id, &u.Provider, &u.Key); err != nil {
				return err
			}
			e := &out[at[task.ID(id)]]
			e.Units = append(e.Units, u)
			return nil
		})
	if err != nil {
		return nil, err
	}
	err = eachRow(ctx, q, `SELECT task_id, name, value, writer, at FROM task_props WHERE task_id IN `+sub+` ORDER BY task_id, name`,
		args, func(r *sql.Rows) error {
			var id, name, body, writer, when string
			if err := r.Scan(&id, &name, &body, &writer, &when); err != nil {
				return err
			}
			var v task.Value
			if err := json.Unmarshal([]byte(body), &v); err != nil {
				return fmt.Errorf("property %s of %s: %w", name, id, err)
			}
			t, err := propTime(when)
			if err != nil {
				return fmt.Errorf("property %s of %s: %w", name, id, err)
			}
			e := &out[at[task.ID(id)]]
			if e.Props == nil {
				e.Props, e.Written = task.Props{}, map[string]task.Written{}
			}
			e.Props[name], e.Written[name] = v, task.Written{By: task.Writer(writer), At: t}
			return nil
		})
	if err != nil {
		return nil, err
	}
	return out, loadProjects(ctx, q, sub, args, out, at)
}

func loadView(ctx context.Context, q querier, v task.View) ([]task.Task, error) {
	where, args := candidates(v)
	return loadTasks(ctx, q, where, args...)
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
	rows, err := q.QueryContext(ctx, `SELECT epoch, rev FROM task_position WHERE only = 1`)
	if err != nil {
		return "", 0, err
	}
	defer rows.Close()
	if rows.Next() {
		err = rows.Scan(&epoch, &rev)
	}
	return epoch, rev, errors.Join(err, rows.Err())
}

func (s taskStore) Tasks(ctx context.Context, q task.Query) ([]task.Task, error) {
	var out []task.Task
	err := s.db.Read(ctx, func(tx *sql.Tx) error {
		all, err := loadView(ctx, tx, q.View)
		out = task.Select(all, q)
		return err
	})
	return out, err
}

func (s taskStore) TasksSince(ctx context.Context, q task.Query, since coretasks.Since) (coretasks.Page, error) {
	var p coretasks.Page
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
			all, err := loadView(ctx, tx, q.View)
			p.Tasks = task.Select(all, q)
			return err
		}
		all, err := loadView(ctx, tx, q.View)
		if err != nil {
			return err
		}
		changed, err := loadTasks(ctx, tx, changedSince, since.Rev)
		if err != nil {
			return err
		}
		held := map[task.ID]bool{}
		for _, e := range all {
			held[e.ID] = true
		}
		revs := map[task.ID]bool{}
		for _, e := range changed {
			revs[e.ID] = true
			if !held[e.ID] {
				all = append(all, e)
			}
		}
		p.Tasks, p.Removed = coretasks.Delta(all, q, func(id task.ID) bool { return revs[id] })
		return nil
	})
	return p, err
}

func (s taskStore) Task(ctx context.Context, id task.ID) (task.Task, error) {
	es, err := loadTasks(ctx, s.db.R(), `id = ?`, string(id))
	if err != nil {
		return task.Task{}, err
	}
	if len(es) == 0 {
		return task.Task{}, fmt.Errorf("%w: %s", coretasks.ErrNoTask, id)
	}
	return es[0], nil
}

func (s taskStore) TasksOf(ctx context.Context, u unit.Unit) ([]task.Task, error) {
	var out []task.Task
	err := s.db.Read(ctx, func(tx *sql.Tx) error {
		var err error
		out, err = loadTasks(ctx, tx, ofUnit, string(u.Provider), u.Key, string(u.Provider), u.Key)
		return err
	})
	return out, err
}

func (s taskStore) Counts(ctx context.Context, area string) (task.Counters, error) {
	var c task.Counters
	err := s.db.Read(ctx, func(tx *sql.Tx) error {
		var err error
		c, err = countsTx(ctx, tx, area)
		return err
	})
	return c, err
}

// nextIDs selects the tasks Next lists: of the ready category, and of a sequential project only its open action
// with the lowest Position (then id), as task.Select does over the candidates of ViewNext. Its arguments are
// nextArgs; area is each task's area property.
const nextIDs = `WITH seq AS (SELECT id FROM tasks WHERE kind = 'project' AND sequential = 1),
first AS (SELECT id FROM (SELECT id, ROW_NUMBER() OVER (PARTITION BY parent ORDER BY position, id) AS n
	FROM tasks WHERE parent IN seq AND ` + isOpen + `) WHERE n = 1),
areas AS (SELECT task_id, json_extract(value, '$.text') AS area FROM task_props WHERE name = ?),
nxt AS (SELECT t.id, COALESCE(a.area, '') AS area FROM tasks t LEFT JOIN areas a ON a.task_id = t.id
	WHERE t.category = ? AND (t.parent NOT IN seq OR t.id IN first)) `

func nextArgs() []any {
	return append(openArgs(), task.PropArea, string(task.CategoryReady))
}

const inArea = `(? = '' OR COALESCE(a.area, '') = ?)`

// countsTx is task.CountsOf over the stored views, one query per map.
func countsTx(ctx context.Context, tx *sql.Tx, area string) (task.Counters, error) {
	c := task.Counters{Area: area, Views: map[task.View]int{task.ViewOutbox: 0}, Areas: map[string]int{}, Contexts: map[string]int{}}
	var focus, inbox, next, waiting, projects, someday, logbook int
	args := nextArgs()
	args = append(args, task.PropFocus, task.FocusOn)
	args = append(args, openArgs()...)
	args = append(args, area, area, string(task.CategoryInbox), area, area, string(task.CategoryWaiting), area, area)
	args = append(args, openArgs()...)
	args = append(args, area, area, string(task.CategoryDeferred), area, area, string(task.CategoryDone), area, area)
	if err := tx.QueryRowContext(ctx, nextIDs+`SELECT
		COALESCE(SUM(t.`+isStarred+` AND t.`+isOpen+` AND `+inArea+`), 0),
		COALESCE(SUM(t.category = ?), 0),
		(SELECT COUNT(*) FROM nxt a WHERE `+inArea+`),
		COALESCE(SUM(t.category = ? AND `+inArea+`), 0),
		COALESCE(SUM(t.kind = 'project' AND t.`+isOpen+` AND `+inArea+`), 0),
		COALESCE(SUM(t.category = ? AND t.snooze_until = '' AND t.snooze_on_activity = 0 AND t.snooze_mute = 0 AND `+inArea+`), 0),
		COALESCE(SUM(t.category = ? AND `+inArea+`), 0)
		FROM tasks t LEFT JOIN areas a ON a.task_id = t.id`, args...).
		Scan(&focus, &inbox, &next, &waiting, &projects, &someday, &logbook); err != nil {
		return task.Counters{}, err
	}
	for v, n := range map[task.View]int{task.ViewFocus: focus, task.ViewInbox: inbox, task.ViewNext: next, task.ViewWaiting: waiting,
		task.ViewProjects: projects, task.ViewSomeday: someday, task.ViewLogbook: logbook} {
		c.Views[v] = n
	}
	err := eachRow(ctx, tx, nextIDs+`SELECT area, COUNT(*) FROM nxt WHERE area != '' GROUP BY area`, nextArgs(), func(r *sql.Rows) error {
		var a string
		var n int
		if err := r.Scan(&a, &n); err != nil {
			return err
		}
		c.Areas[a] = n
		return nil
	})
	if err != nil {
		return task.Counters{}, err
	}
	err = eachRow(ctx, tx, nextIDs+`SELECT j.value, COUNT(*) FROM nxt
		JOIN task_props x ON x.task_id = nxt.id AND x.name = ?, json_each(x.value, '$.list') j
		WHERE (? = '' OR nxt.area = ?) GROUP BY j.value`, append(nextArgs(), task.PropContexts, area, area), func(r *sql.Rows) error {
		var cx string
		var n int
		if err := r.Scan(&cx, &n); err != nil {
			return err
		}
		c.Contexts[cx] = n
		return nil
	})
	if err != nil {
		return task.Counters{}, err
	}
	c.Badge = c.Views[task.ViewInbox] + c.Views[task.ViewOutbox]
	return c, nil
}

func newEpoch() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (s taskStore) Apply(ctx context.Context, cs []task.Change, evs ...port.Event) error {
	for _, ev := range evs {
		if ev.At.IsZero() {
			return fmt.Errorf("the %s event has no time", ev.Kind)
		}
	}
	_, err := s.db.Write(ctx, func(tx *Tx) error {
		changed := map[task.ID]task.Task{}
		var order []task.ID
		for _, c := range cs {
			e := c.Task
			if e.ID == "" {
				return errors.New("a task without an id")
			}
			old, err := loadTasks(ctx, tx, `id = ?`, string(e.ID))
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
		if len(changed) == 0 && len(cs) > 0 {
			return nil
		}
		if len(changed) == 0 {
			return writeEvents(ctx, tx, evs)
		}
		epoch, rev, err := readPosition(ctx, tx)
		if err != nil {
			return err
		}
		if epoch == "" {
			epoch = newEpoch()
		}
		rev++
		if _, err := tx.ExecContext(ctx, `INSERT INTO task_position (only, epoch, rev) VALUES (1, ?, ?)
			ON CONFLICT (only) DO UPDATE SET epoch = excluded.epoch, rev = excluded.rev`, epoch, rev); err != nil {
			return err
		}
		for _, id := range order {
			e, ok := changed[id]
			if !ok {
				continue
			}
			if err := writeTask(ctx, tx, e, rev); err != nil {
				return err
			}
			tx.Touch(port.TopicTasks, taskUnits(e)...)
		}
		return writeEvents(ctx, tx, evs)
	})
	return err
}

func taskUnits(e task.Task) []unit.Unit {
	out := append([]unit.Unit(nil), e.Units...)
	for _, r := range e.Items {
		out = append(out, r.Unit)
	}
	return out
}

func writeTask(ctx context.Context, tx *Tx, e task.Task, rev uint64) error {
	waiting := ""
	if e.Waiting != nil {
		b, err := json.Marshal(e.Waiting)
		if err != nil {
			return err
		}
		waiting = string(b)
	}
	for _, table := range []string{"task_items", "task_units", "task_props"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE task_id = ?`, string(e.ID)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, upsertTaskSQL,
		string(e.ID), string(e.Kind), string(e.State), string(e.Category), e.Title, e.Notes, string(e.Parent), e.Position,
		e.Sequential, eventTime(e.Snooze.Until), e.Snooze.OnActivity, string(e.Snooze.Return), e.Snooze.Mute, waiting,
		e.Changed, eventTime(e.SeenAt), e.Origin, eventTime(e.Created), eventTime(e.Updated),
		int64(e.ReviewEvery/time.Second), eventTime(e.ReviewedAt), rev); err != nil {
		return err
	}
	for _, r := range e.Items {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO task_items (task_id, provider, unit_key, item) VALUES (?, ?, ?, ?)`,
			string(e.ID), string(r.Unit.Provider), r.Unit.Key, r.Item); err != nil {
			return err
		}
	}
	for i, u := range e.Units {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO task_units (task_id, provider, unit_key, ord) VALUES (?, ?, ?, ?)`,
			string(e.ID), string(u.Provider), u.Key, i); err != nil {
			return err
		}
	}
	for _, name := range slices.Sorted(maps.Keys(e.Props)) {
		v := e.Props[name]
		if v.IsZero() {
			continue
		}
		body, err := json.Marshal(v)
		if err != nil {
			return err
		}
		w := e.Written[name]
		if _, err := tx.ExecContext(ctx, upsertPropSQL, string(e.ID), name, string(body), string(cmp.Or(w.By, task.WriterPerson)),
			eventTime(cmp.Or(w.At, e.Updated))); err != nil {
			return fmt.Errorf("property %s of %s: %w", name, e.ID, err)
		}
	}
	return writeProject(ctx, tx, e)
}

var _ coretasks.Recategorizer = taskStore{}

const (
	heldStatesSQL    = `SELECT state, category FROM tasks GROUP BY state, category ORDER BY state, category`
	recategorizeSQL  = `UPDATE tasks SET category = ?, rev = ? WHERE state = ? AND category != ?`
	recategorizedSQL = `SELECT id FROM tasks WHERE state = ? AND rev = ?`
)

// Recategorize gives every stored task the category of its state as of reads it, moving the tasks it changes to
// a new rev and noting them; a state of says nothing of is unknown, and its tasks keep their category.
func (s taskStore) Recategorize(ctx context.Context, of func(state string) (task.Category, bool)) ([]string, error) {
	var unknown []string
	_, err := s.db.Write(ctx, func(tx *Tx) error {
		unknown = nil
		type pair struct{ state, category string }
		var held []pair
		if err := eachRow(ctx, tx, heldStatesSQL, nil, func(r *sql.Rows) error {
			var p pair
			if err := r.Scan(&p.state, &p.category); err != nil {
				return err
			}
			held = append(held, p)
			return nil
		}); err != nil {
			return err
		}
		var rev uint64
		for _, p := range held {
			c, ok := of(p.state)
			if !ok {
				if !slices.Contains(unknown, p.state) {
					unknown = append(unknown, p.state)
				}
				continue
			}
			if string(c) == p.category {
				continue
			}
			if rev == 0 {
				epoch, at, err := readPosition(ctx, tx)
				if err != nil {
					return err
				}
				if epoch == "" {
					epoch = newEpoch()
				}
				rev = at + 1
				if _, err := tx.ExecContext(ctx, `INSERT INTO task_position (only, epoch, rev) VALUES (1, ?, ?)
					ON CONFLICT (only) DO UPDATE SET epoch = excluded.epoch, rev = excluded.rev`, epoch, rev); err != nil {
					return err
				}
			}
			if _, err := tx.ExecContext(ctx, recategorizeSQL, string(c), rev, p.state, string(c)); err != nil {
				return fmt.Errorf("recategorizing %s: %w", p.state, err)
			}
			if err := eachRow(ctx, tx, recategorizedSQL, []any{p.state, rev}, func(r *sql.Rows) error {
				var id string
				if err := r.Scan(&id); err != nil {
					return err
				}
				tx.Touch(port.TopicTasks, task.UnitOf(task.ID(id)))
				return nil
			}); err != nil {
				return err
			}
		}
		return nil
	})
	return unknown, err
}
