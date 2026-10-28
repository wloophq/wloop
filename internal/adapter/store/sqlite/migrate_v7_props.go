package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

// v7Props adds the tasks' category and their properties.
func v7Props(ctx context.Context, tx *sql.Tx) error {
	for _, q := range []string{
		`ALTER TABLE tasks ADD COLUMN category TEXT NOT NULL DEFAULT ''`,
		`CREATE TABLE task_props (
  task_id TEXT NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
  name TEXT NOT NULL, value TEXT NOT NULL, writer TEXT NOT NULL, at TEXT NOT NULL,
  PRIMARY KEY (task_id, name)
) WITHOUT ROWID`,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("%s: %w", q, err)
		}
	}
	return nil
}
