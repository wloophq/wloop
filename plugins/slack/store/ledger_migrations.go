package store

import (
	"context"
	"time"
)

const (
	migratedSQL     = `SELECT 1 FROM ledger_migrations WHERE name = ?`
	markMigratedSQL = `INSERT INTO ledger_migrations (name, at) VALUES (?, ?) ON CONFLICT (name) DO NOTHING`
)

// Migrated says whether the ledger migration name was applied.
func (s *ExtractionStore) Migrated(ctx context.Context, name string) (bool, error) {
	rows, err := s.db.R().QueryContext(ctx, migratedSQL, name)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	return rows.Next(), rows.Err()
}

// MarkMigrated records that the ledger migration name was applied at at.
func (s *ExtractionStore) MarkMigrated(ctx context.Context, name string, at time.Time) error {
	_, err := s.db.Write(ctx, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx, markMigratedSQL, name, formatTime(at))
		return err
	})
	return err
}
