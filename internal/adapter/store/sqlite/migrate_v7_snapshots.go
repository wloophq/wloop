package sqlite

import (
	"context"
	"database/sql"
)

// v7Snapshots creates unit_snapshots, the last snapshot each source plugin reported of each of its units.
func v7Snapshots(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `CREATE TABLE unit_snapshots (
  provider TEXT NOT NULL, unit_key TEXT NOT NULL,
  snapshot TEXT NOT NULL, rev INTEGER NOT NULL, at TEXT NOT NULL,
  PRIMARY KEY (provider, unit_key)
) WITHOUT ROWID`)
	return err
}
