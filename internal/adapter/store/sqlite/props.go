package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/task"
)

// PropStore is the tasks' properties; now stamps each write.
type PropStore struct {
	db  *DB
	now func() time.Time
}

var _ coretasks.PropStore = PropStore{}

// Props is the store of the tasks' properties.
func (db *DB) Props(now func() time.Time) PropStore { return PropStore{db, now} }

const upsertPropSQL = `INSERT INTO task_props (task_id, name, value, writer, at) VALUES (?, ?, ?, ?, ?)
	ON CONFLICT (task_id, name) DO UPDATE SET value = excluded.value, writer = excluded.writer, at = excluded.at`

// SetProp writes the property and moves the task to a new rev, so a page
// since an older one lists it again.
func (s PropStore) SetProp(ctx context.Context, id task.ID, name string, v task.Value, w task.Writer) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.Write(ctx, func(tx *Tx) error {
		var one int
		switch err := tx.QueryRowContext(ctx, `SELECT 1 FROM tasks WHERE id = ?`, string(id)).Scan(&one); {
		case errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("%w: %s", coretasks.ErrNoTask, id)
		case err != nil:
			return err
		}
		if _, err := tx.ExecContext(ctx, upsertPropSQL, string(id), name, string(body), string(w), formatTime(s.now())); err != nil {
			return fmt.Errorf("property %s of %s: %w", name, id, err)
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
		if _, err := tx.ExecContext(ctx, `UPDATE tasks SET rev = ? WHERE id = ?`, rev, string(id)); err != nil {
			return err
		}
		tx.Touch(port.TopicTasks, task.UnitOf(id))
		return nil
	})
	return err
}

func (s PropStore) PropsOf(ctx context.Context, id task.ID) (task.Props, error) {
	out := task.Props{}
	err := s.db.Read(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT name, value FROM task_props WHERE task_id = ?`, string(id))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var name, body string
			if err := rows.Scan(&name, &body); err != nil {
				return err
			}
			var v task.Value
			if err := json.Unmarshal([]byte(body), &v); err != nil {
				return fmt.Errorf("property %s of %s: %w", name, id, err)
			}
			out[name] = v
		}
		return rows.Err()
	})
	return out, err
}
