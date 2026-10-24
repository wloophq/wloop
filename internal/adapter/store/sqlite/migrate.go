package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"
)

// migration moves the store from to-1 to to, in one transaction.
type migration struct {
	to uint32
	up func(tx *sql.Tx) error
}

// migrations are in order, the last one's to being SchemaVersion.
var migrations = []migration{
	{2, draftShape},
}

// migrate runs on w the migrations past version v, after a backup of the
// store when backup is set, then truncates the WAL so the file's header,
// which Check reads, carries the new version.
func migrate(w *sql.DB, path string, v uint32, backup bool) error {
	if v == SchemaVersion {
		return nil
	}
	if backup {
		if err := backupStore(w, path, v); err != nil {
			return err
		}
	}
	for _, m := range migrations {
		if m.to <= v {
			continue
		}
		if err := runMigration(w, m); err != nil {
			return fmt.Errorf("migrating the store to version %d: %w", m.to, err)
		}
	}
	_, err := w.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}

func runMigration(w *sql.DB, m migration) error {
	tx, err := w.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := m.up(tx); err != nil {
		return err
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, m.to)); err != nil {
		return err
	}
	return tx.Commit()
}

// backupStore copies the store at version v beside it; a file copy is not
// safe under WAL. An existing backup is kept and the new one takes the time.
func backupStore(w *sql.DB, path string, v uint32) error {
	dst := fmt.Sprintf("%s.v%d.bak", path, v)
	if _, err := os.Stat(dst); !errors.Is(err, os.ErrNotExist) {
		dst = fmt.Sprintf("%s.v%d.%d.bak", path, v, time.Now().Unix())
	}
	if _, err := w.Exec(`VACUUM INTO ?`, dst); err != nil {
		return fmt.Errorf("backing up the store to %s: %w", dst, err)
	}
	return os.Chmod(dst, storeMode)
}

// draftShape gives drafts a purpose, a delivery and the effects of their
// sending, from their kind, and records on the cursor the closures sent.
func draftShape(tx *sql.Tx) error {
	for _, q := range []string{
		`ALTER TABLE drafts ADD COLUMN purpose TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE drafts ADD COLUMN delivery TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE drafts ADD COLUMN effects TEXT NOT NULL DEFAULT '[]'`,
		`UPDATE drafts SET purpose = kind, delivery = CASE kind WHEN 'react' THEN 'reaction' WHEN 'comment' THEN 'comment' ELSE 'message' END`,
		`UPDATE drafts SET effects = '["entry.nudged"]' WHERE kind = 'nudge' AND state IN ('proposed', 'approved', 'sending', 'failed')`,
		`UPDATE drafts SET effects = '["unit.closed"]' WHERE kind = 'close' AND state IN ('proposed', 'approved', 'sending', 'failed')`,
		`UPDATE extraction_cursors SET closure_posted = 1 WHERE closure_posted = 0 AND EXISTS (
			SELECT 1 FROM drafts d WHERE d.provider = extraction_cursors.provider AND d.unit_key = extraction_cursors.unit_key
			AND d.kind = 'close' AND d.state = 'sent')`,
	} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	return nil
}
