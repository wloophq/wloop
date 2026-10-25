package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/mark"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// saveMark sets u's mark name to value; an empty value removes it.
func saveMark(ctx context.Context, tx *Tx, u unit.Unit, name, value string) error {
	var err error
	if value == "" {
		_, err = tx.ExecContext(ctx, `DELETE FROM unit_marks WHERE provider = ? AND unit_key = ? AND mark = ?`, string(u.Provider), u.Key, name)
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO unit_marks (provider, unit_key, mark, value) VALUES (?, ?, ?, ?)
			ON CONFLICT (provider, unit_key, mark) DO UPDATE SET value = excluded.value`, string(u.Provider), u.Key, name, value)
	}
	if err == nil {
		tx.Touch(port.TopicRounds, u)
	}
	return err
}

// recordMark sets u's mark name to v, keeping a stored watermark v does
// not pass (mark.Merge).
func recordMark(ctx context.Context, tx *Tx, u unit.Unit, name string, v mark.Value) error {
	stored := ""
	if v.Watermark {
		err := tx.QueryRowContext(ctx, `SELECT value FROM unit_marks WHERE provider = ? AND unit_key = ? AND mark = ?`, string(u.Provider), u.Key, name).Scan(&stored)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	return saveMark(ctx, tx, u, name, mark.Merge(stored, v))
}

// Marks are u's handled marks by name.
func (db *DB) Marks(ctx context.Context, u unit.Unit) (map[string]string, error) {
	rows, err := db.R().QueryContext(ctx, `SELECT mark, value FROM unit_marks WHERE provider = ? AND unit_key = ?`, string(u.Provider), u.Key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var name, value string
		if err := rows.Scan(&name, &value); err != nil {
			return nil, err
		}
		out[name] = value
	}
	return out, rows.Err()
}
