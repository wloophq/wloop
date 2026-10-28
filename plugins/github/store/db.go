// Package store is the GitHub plugin's own database, in the state
// directory the host gives it: the pull requests it reads, their sightings
// and details, the workspaces' fetches, its meta values and the overrides
// it keeps of what the user set.
package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/wloophq/wloop/pkg/plugin/store"
	"github.com/wloophq/wloop/plugins/github/pr"
)

// TimeLayout is how the store writes a time, the host's layout, so an
// imported time reads and its text sorts as the store's own.
const TimeLayout = "2006-01-02T15:04:05.000000000Z"

// Change is what one write transaction changed: the pull requests whose
// reported facts it moved.
type Change struct {
	Refs []pr.Ref
}

// DB is the plugin's store. Every write runs under one lock, and each
// commit that changed pull requests is heard by the OnChange listeners, in
// commit order.
type DB struct {
	db *sql.DB

	mu       sync.Mutex
	listenMu sync.Mutex
	listen   []func(Change)

	useMu sync.Mutex
	host  Host
	clock func() time.Time
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

func (db *DB) Close() error { return db.db.Close() }

// OnChange makes f hear every later commit that changed pull requests.
func (db *DB) OnChange(f func(Change)) {
	db.listenMu.Lock()
	defer db.listenMu.Unlock()
	db.listen = append(db.listen, f)
}

// Tx is a write transaction that records the pull requests it changed.
type Tx struct {
	*sql.Tx
	refs []pr.Ref
}

// Touch records that the transaction changed the reported facts of refs.
func (tx *Tx) Touch(refs ...pr.Ref) { tx.refs = append(tx.refs, refs...) }

// touchChanged touches ref when the statement that returned res and err
// changed a row.
func (tx *Tx) touchChanged(res sql.Result, err error, ref pr.Ref) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n > 0 {
		tx.Touch(ref)
	}
	return nil
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
	if len(tx.refs) == 0 {
		return Change{}, nil
	}
	refs := slices.Clone(tx.refs)
	slices.SortFunc(refs, pr.Compare)
	c := Change{Refs: slices.Compact(refs)}
	db.listenMu.Lock()
	listen := slices.Clone(db.listen)
	db.listenMu.Unlock()
	for _, f := range listen {
		f(c)
	}
	return c, nil
}

// reader is what a single read statement needs.
type reader interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type scanner interface{ Scan(dest ...any) error }

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(TimeLayout)
}

func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(TimeLayout, s)
}

func noRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }
