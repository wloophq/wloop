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
	"github.com/wloophq/wloop/internal/domain/dossier"
	"github.com/wloophq/wloop/internal/domain/event"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type taskStore struct{ db *DB }

var _ coretasks.Store = taskStore{}

func (db *DB) Tasks() coretasks.Store { return taskStore{db} }

const taskFields = `id, state, category, title, notes, parent, position, sequential,
	changed, seen_at, origin, created, updated, review_every, reviewed_at`

// hasWake holds a task that holds a wake.
const hasWake = `EXISTS (SELECT 1 FROM wakes WHERE wakes.task_id = tasks.id)`

// upsertTaskSQL updates an existing task in place: a REPLACE would delete
// the row first and cascade into the rows keyed by it.
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
	isOpen = `category NOT IN (?, ?)`
)

func openArgs() []any { return []any{string(task.CategoryDone), string(task.CategoryDropped)} }

// selected is the SQL condition on tasks, with its arguments, of the tasks q selects: every one, or the open ones.
func selected(q task.Query) (string, []any) {
	if q.All {
		return `1`, nil
	}
	return isOpen, openArgs()
}

func taskTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(event.TimeLayout, s)
}

func scanTask(row scanner) (task.Task, error) {
	var e task.Task
	var seenAt, created, updated, reviewed string
	var state, category, parent string
	var every int64
	if err := row.Scan(&e.ID, &state, &category, &e.Title, &e.Notes, &parent,
		&e.Position, &e.Sequential, &e.Changed, &seenAt, &e.Origin, &created, &updated, &every, &reviewed,
		&e.Asleep); err != nil {
		return task.Task{}, err
	}
	e.State, e.Category, e.Parent = task.State(state), task.Category(category), task.ID(parent)
	e.ReviewEvery = time.Duration(every) * time.Second
	for _, f := range []struct {
		dst *time.Time
		raw string
	}{{&e.SeenAt, seenAt}, {&e.Created, created}, {&e.Updated, updated}, {&e.ReviewedAt, reviewed}} {
		t, err := taskTime(f.raw)
		if err != nil {
			return task.Task{}, fmt.Errorf("a time of task %s: %w", e.ID, err)
		}
		*f.dst = t
	}
	return e, nil
}

// loadTasks reads the tasks matching where, with their items, units and properties, oldest first.
func loadTasks(ctx context.Context, q querier, where string, args ...any) ([]task.Task, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+taskFields+`, `+hasWake+` FROM tasks WHERE `+where+taskOrder, args...)
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
	err = eachRow(ctx, q, `SELECT task_id, provider, unit_key, item, resolved FROM task_items WHERE task_id IN `+sub+
		` ORDER BY task_id, provider, unit_key, item`, args, func(r *sql.Rows) error {
		var id string
		var ref task.ItemRef
		var resolved bool
		if err := r.Scan(&id, &ref.Unit.Provider, &ref.Unit.Key, &ref.Item, &resolved); err != nil {
			return err
		}
		e := &out[at[task.ID(id)]]
		if resolved {
			e.Resolved = append(e.Resolved, ref)
		} else {
			e.Items = append(e.Items, ref)
		}
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
			t, err := taskTime(when)
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
	return out, nil
}

func loadSelected(ctx context.Context, tx querier, q task.Query) ([]task.Task, error) {
	where, args := selected(q)
	return loadTasks(ctx, tx, where, args...)
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
		var err error
		out, err = loadSelected(ctx, tx, q)
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
		if since.Epoch != p.Epoch || since.Rev > p.Rev {
			p.Full = true
			p.Tasks, err = loadSelected(ctx, tx, q)
			return err
		}
		p.Tasks, err = loadTasks(ctx, tx, changedSince, since.Rev)
		return err
	})
	return p, err
}

// Task reads task id, or, when no task has it, the task an alias of id leads to, an open one first: one hop,
// since every alias leads to a task.
func (s taskStore) Task(ctx context.Context, id task.ID) (task.Task, error) {
	es, err := loadTasks(ctx, s.db.R(), `id = ?`, string(id))
	if err == nil && len(es) == 0 {
		es, err = loadTasks(ctx, s.db.R(), `id = (SELECT a.new_id FROM task_aliases a JOIN tasks t ON t.id = a.new_id
			WHERE a.old_id = ? ORDER BY t.`+isOpen+` DESC, t.created, t.id LIMIT 1)`, append([]any{string(id)}, openArgs()...)...)
	}
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
			if len(old) == 1 {
				e.Asleep = old[0].Asleep
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
			if !e.Open() {
				if err := dropWake(ctx, tx, e.ID); err != nil {
					return err
				}
			}
			tx.Touch(port.TopicTasks, append(taskUnits(e), task.UnitOf(e.ID))...)
			if err := adoptUnits(ctx, tx, e); err != nil {
				return err
			}
		}
		return writeEvents(ctx, tx, evs)
	})
	return err
}

// adoptUnits gives open task e what waited on its units for a task to hold them: the gates opened on a unit
// before any task held it, and the unit's own dossier, whose directory move dossier_moves records.
func adoptUnits(ctx context.Context, tx *Tx, e task.Task) error {
	if !e.Open() {
		return nil
	}
	for _, u := range taskUnits(e) {
		if u.Provider == task.UnitOf(e.ID).Provider {
			continue
		}
		res, err := tx.ExecContext(ctx, `UPDATE gates SET task_id = ? WHERE task_id IS NULL AND provider = ? AND unit_key = ?`,
			string(e.ID), string(u.Provider), u.Key)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n > 0 {
			tx.Touch(port.TopicGates, u, task.UnitOf(e.ID))
		}
		if err := giveDossier(ctx, tx.Tx, dossier.Owner{Unit: u}.String(), dossier.Owner{Unit: u}.String(), string(e.ID)); err != nil {
			return err
		}
	}
	return nil
}

func taskUnits(e task.Task) []unit.Unit {
	out := append([]unit.Unit(nil), e.Units...)
	for _, r := range e.Items {
		out = append(out, r.Unit)
	}
	return out
}

func writeTask(ctx context.Context, tx *Tx, e task.Task, rev uint64) error {
	for _, table := range []string{"task_items", "task_units", "task_props"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE task_id = ?`, string(e.ID)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, upsertTaskSQL,
		string(e.ID), string(e.State), string(e.Category), e.Title, e.Notes, string(e.Parent), e.Position,
		e.Sequential, e.Changed, eventTime(e.SeenAt), e.Origin, eventTime(e.Created), eventTime(e.Updated),
		int64(e.ReviewEvery/time.Second), eventTime(e.ReviewedAt), rev); err != nil {
		return err
	}
	for _, rs := range []struct {
		refs     []task.ItemRef
		resolved bool
	}{{e.Items, false}, {e.Resolved, true}} {
		for _, r := range rs.refs {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO task_items (task_id, provider, unit_key, item, resolved) VALUES (?, ?, ?, ?, ?)`,
				string(e.ID), string(r.Unit.Provider), r.Unit.Key, r.Item, rs.resolved); err != nil {
				return err
			}
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
	return nil
}

var _ coretasks.Recategorizer = taskStore{}

const (
	heldStatesSQL    = `SELECT state, category FROM tasks GROUP BY state, category ORDER BY state, category`
	recategorizeSQL  = `UPDATE tasks SET category = ?, rev = ? WHERE state = ? AND category != ?`
	recategorizedSQL = `SELECT 'task', id FROM tasks WHERE state = ?1 AND rev = ?2
		UNION SELECT u.provider, u.unit_key FROM task_units u JOIN tasks t ON t.id = u.task_id WHERE t.state = ?1 AND t.rev = ?2
		UNION SELECT i.provider, i.unit_key FROM task_items i JOIN tasks t ON t.id = i.task_id WHERE t.state = ?1 AND t.rev = ?2`
)

// Recategorize gives every stored task the category of its state as of reads it, moving the tasks it changes to
// a new rev and noting each one's unit and the units Apply notes for it; a state of says nothing of is unknown, and its tasks keep their category.
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
				var u unit.Unit
				if err := r.Scan(&u.Provider, &u.Key); err != nil {
					return err
				}
				tx.Touch(port.TopicTasks, u)
				return nil
			}); err != nil {
				return err
			}
		}
		return nil
	})
	return unknown, err
}
