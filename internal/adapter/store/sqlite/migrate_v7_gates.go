package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

// v7Gates adds the gates and the gate an answer answers.
func v7Gates(ctx context.Context, tx *sql.Tx) error {
	for _, q := range []string{
		`CREATE TABLE gates (
  id INTEGER PRIMARY KEY,
  task_id TEXT REFERENCES tasks (id),
  provider TEXT NOT NULL DEFAULT '', unit_key TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL, name TEXT NOT NULL DEFAULT '', subject TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL, intents TEXT NOT NULL, answer TEXT NOT NULL DEFAULT '',
  by_actor TEXT NOT NULL DEFAULT '', rule TEXT NOT NULL DEFAULT '',
  deadline TEXT NOT NULL DEFAULT '', default_intent TEXT NOT NULL DEFAULT '',
  opened_at TEXT NOT NULL, closed_at TEXT NOT NULL DEFAULT ''
)`,
		`ALTER TABLE turn_answers ADD COLUMN gate_id INTEGER NOT NULL DEFAULT 0`,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("%s: %w", q, err)
		}
	}
	return nil
}
