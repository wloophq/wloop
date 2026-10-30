// Package store is the Slack plugin's own database, in the state directory
// the host gives it: the conversations, threads, messages and users it
// read, the DM segment map and what the extraction keeps per unit.
package store

import (
	"context"
	"database/sql"
	"slices"
	"sync"
	"time"

	"github.com/wloophq/wloop/pkg/plugin/store"
	"github.com/wloophq/wloop/plugins/slack/unit"
)

// TimeLayout is how the store writes a time.
const TimeLayout = time.RFC3339Nano

// Change is what one write transaction changed: the units whose reported
// facts it moved, or every unit when All.
type Change struct {
	Units []unit.Unit
	All   bool
}

// DB is the plugin's store. Every write runs under one lock, and each
// commit that changed units is heard by the OnChange listeners, in commit
// order.
type DB struct {
	db *sql.DB

	mu       sync.Mutex
	listenMu sync.Mutex
	listen   []func(Change)
}

// Open opens dir's store, creating it with the plugin's schema, and imports
// the import.db the host's store migration exported once.
func Open(ctx context.Context, dir string) (*DB, error) {
	db, err := store.Open(ctx, dir, store.Options{Migrations: migrations, Import: importTables})
	if err != nil {
		return nil, err
	}
	return &DB{db: db}, nil
}

// OpenDB is a store over an open database whose schema is current; tests
// open one in memory.
func OpenDB(db *sql.DB) *DB { return &DB{db: db} }

func (db *DB) Close() error { return db.db.Close() }

// OnChange makes f hear every later commit that changed units.
func (db *DB) OnChange(f func(Change)) {
	db.listenMu.Lock()
	defer db.listenMu.Unlock()
	db.listen = append(db.listen, f)
}

// Reader is what a single read statement needs.
type Reader interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// R reads with one statement.
func (db *DB) R() Reader { return db.db }

// Read runs fn in one read transaction, so every statement sees one
// snapshot; fn must not write.
func (db *DB) Read(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return fn(tx)
}

// Tx is a write transaction that records the units it changed.
type Tx struct {
	*sql.Tx
	units []unit.Unit
	all   bool
}

// Touch records that the transaction changed the reported facts of units;
// none is a change of no unit in particular.
func (tx *Tx) Touch(units ...unit.Unit) {
	if len(units) == 0 {
		tx.all = true
		return
	}
	tx.units = append(tx.units, units...)
}

// Write runs fn in one write transaction. After the commit, the listeners
// hear what fn touched; an error from fn rolls back and tells nobody.
func (db *DB) Write(ctx context.Context, fn func(tx *Tx) error) (Change, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	stx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return Change{}, err
	}
	defer stx.Rollback()
	tx := &Tx{Tx: stx}
	if err := fn(tx); err != nil {
		return Change{}, err
	}
	if err := stx.Commit(); err != nil {
		return Change{}, err
	}
	if len(tx.units) == 0 && !tx.all {
		return Change{}, nil
	}
	units := slices.Clone(tx.units)
	slices.SortFunc(units, unit.Compare)
	c := Change{Units: slices.Compact(units), All: tx.all}
	db.listenMu.Lock()
	listen := slices.Clone(db.listen)
	db.listenMu.Unlock()
	for _, f := range listen {
		f(c)
	}
	return c, nil
}

type scanner interface{ Scan(dest ...any) error }

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(TimeLayout)
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(TimeLayout, s)
	return t
}
