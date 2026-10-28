// Package store is a plugin's own SQLite database, in the state directory
// the host gives it at start: opened with the plugin's migrations, and
// filled once from the import.db the host's store migration exported for it.
// The host never reads it.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // the SQLite driver
)

const (
	// File is the store's name in the plugin's state directory.
	File = "plugin.db"
	// ImportFile is what the host's store migration exported for the plugin.
	ImportFile = "import.db"
	// Imported is ImportFile once imported.
	Imported = "import.db.done"
	// ImportSchema is the schema ImportFile is attached as while Import runs.
	ImportSchema = "import"
)

// Migration moves the store to Version, which counts from 1 without gaps.
type Migration struct {
	Version int
	Up      func(ctx context.Context, tx *sql.Tx) error
}

// Options are the plugin's migrations and its import.
type Options struct {
	Migrations []Migration
	// Import copies what the plugin keeps from ImportSchema's tables, in the
	// transaction that records the import in the store; nil imports nothing.
	Import func(ctx context.Context, tx *sql.Tx) error
}

// Open opens dir's store, applies the migrations it lacks, then imports
// dir's ImportFile once and renames it Imported. The store records the
// import in the import's own transaction, so an ImportFile left in place by
// a crash before the rename is renamed and never imported again. A failed
// migration or import leaves the store as it was and the file in place.
func Open(ctx context.Context, dir string, opts Options) (*sql.DB, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, File)
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := os.Chmod(path, 0o600); err != nil && !errors.Is(err, fs.ErrNotExist) {
		db.Close()
		return nil, err
	}
	if err := migrate(ctx, db, opts.Migrations); err != nil {
		db.Close()
		return nil, err
	}
	if err := importOnce(ctx, db, dir, opts.Import); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func migrate(ctx context.Context, db *sql.DB, ms []Migration) error {
	var at int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&at); err != nil {
		return err
	}
	for i, m := range ms {
		if m.Version != i+1 {
			return fmt.Errorf("store: migration %d is numbered %d", i+1, m.Version)
		}
		if m.Version <= at {
			continue
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if err := m.Up(ctx, tx); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("store: migration %d: %w", m.Version, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", m.Version)); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// importsTable records the import of ImportFile, in the store.
const importsTable = "plugin_store_import"

func importOnce(ctx context.Context, db *sql.DB, dir string, imp func(context.Context, *sql.Tx) error) error {
	from := filepath.Join(dir, ImportFile)
	if _, err := os.Stat(from); errors.Is(err, fs.ErrNotExist) || imp == nil {
		return nil
	}
	if _, err := db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+importsTable+" (file TEXT NOT NULL, at TEXT NOT NULL)"); err != nil {
		return err
	}
	var done int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+importsTable).Scan(&done); err != nil {
		return err
	}
	if done == 0 {
		if err := importFrom(ctx, db, from, imp); err != nil {
			return err
		}
	}
	return os.Rename(from, filepath.Join(dir, Imported))
}

func importFrom(ctx context.Context, db *sql.DB, from string, imp func(context.Context, *sql.Tx) error) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "ATTACH DATABASE ? AS "+ImportSchema, "file:"+from+"?mode=ro"); err != nil {
		return err
	}
	defer func() { _, _ = conn.ExecContext(context.WithoutCancel(ctx), "DETACH DATABASE "+ImportSchema) }()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := imp(ctx, tx); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("store: importing %s: %w", from, err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO "+importsTable+" (file, at) VALUES (?, ?)", ImportFile, time.Now().UTC().Format(time.RFC3339)); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
