package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// ReadConns is the size of the read pool.
const ReadConns = 4

const pragmas = "_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)" +
	"&_pragma=temp_store(MEMORY)&_pragma=cache_size(-8000)&_pragma=mmap_size(67108864)" +
	"&_pragma=journal_size_limit(8388608)&_pragma=foreign_keys(1)"

const (
	writerDSN = "?" + pragmas + "&_txlock=immediate"
	readerDSN = "?" + pragmas + "&_pragma=query_only(1)&_txlock=deferred"
)

// Noter is what Write notes into; *change.Log satisfies it.
type Noter interface {
	Note(t port.Topic, units ...unit.Unit) port.Change
	NoteAll(t port.Topic) port.Change
}

// DB is the store: one writer connection, reached only through Write, and a
// read-only pool of ReadConns connections.
type DB struct {
	w, r *sql.DB
	mu   sync.Mutex
	// holder is the goroutine that holds mu, for the nested-Write panic.
	holder atomic.Uint64
	log    Noter
}

// Open opens the store at path, creating it at SchemaVersion when it holds
// no table, and refuses with a SchemaError a store it cannot open as it is:
// an older one, which Migrate takes first, or a newer one that is not
// compatible. Both handles open through connector. A nil log gets a private
// change log.
func Open(path string, log Noter) (*DB, error) { return current.open(path, log) }

func (set schemaSet) open(path string, log Noter) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := set.createIfEmpty(path); err != nil {
		return nil, err
	}
	if err := tighten(path); err != nil {
		return nil, err
	}
	w, err := handle(path+writerDSN, 1)
	if err != nil {
		return nil, err
	}
	var v uint32
	if err := w.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		w.Close()
		return nil, err
	}
	status := Status{Version: v, Compatible: v}
	if v > set.version {
		if status.Compatible, err = readCompatible(context.Background(), w, v); err != nil {
			w.Close()
			return nil, err
		}
	}
	if err := set.refuse(path, status); err != nil {
		w.Close()
		return nil, err
	}
	if _, err := w.Exec(`PRAGMA optimize=0x10002`); err != nil {
		w.Close()
		return nil, err
	}
	r, err := handle(path+readerDSN, ReadConns)
	if err != nil {
		w.Close()
		return nil, err
	}
	if err := tighten(path); err != nil {
		w.Close()
		r.Close()
		return nil, err
	}
	if log == nil {
		log = change.NewLog(change.DefaultWindow, nil)
	}
	return &DB{w: w, r: r, log: log}, nil
}

// storeMode is the mode of the store and its WAL and shared memory: they
// hold the user's work.
const storeMode = 0o600

// tighten sets the store at path and the WAL and shared memory beside it
// to storeMode; SQLite creates the latter two with the store's mode.
func tighten(path string) error {
	for _, f := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(f, storeMode); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func handle(dsn string, conns int) (*sql.DB, error) {
	c, err := connector(dsn)
	if err != nil {
		return nil, err
	}
	db := sql.OpenDB(c)
	db.SetMaxOpenConns(conns)
	db.SetMaxIdleConns(conns)
	return db, nil
}

// closeWait bounds how long Close waits for statements in flight.
const closeWait = 2 * time.Second

// Close closes both handles and waits, up to closeWait, for the statements
// in flight to release their connections, so the store's files stop
// changing once it returns. Connections still open then are an error.
func (db *DB) Close() error {
	err := errors.Join(db.r.Close(), db.w.Close())
	for deadline := time.Now().Add(closeWait); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		if db.r.Stats().OpenConnections+db.w.Stats().OpenConnections == 0 {
			return err
		}
	}
	n := db.r.Stats().OpenConnections + db.w.Stats().OpenConnections
	return errors.Join(err, fmt.Errorf("sqlite: %d connections still open %v after Close", n, closeWait))
}

// Reader is what a single read statement needs. An Exec through the pool
// fails with SQLITE_READONLY.
type Reader interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type pinKey struct{ db *DB }

func (db *DB) pinned(ctx context.Context) *sql.Tx {
	tx, _ := ctx.Value(pinKey{db}).(*sql.Tx)
	return tx
}

type reader struct{ db *DB }

func (r reader) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if tx := r.db.pinned(ctx); tx != nil {
		return tx.QueryContext(ctx, query, args...)
	}
	return r.db.r.QueryContext(ctx, query, args...)
}

func (r reader) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if tx := r.db.pinned(ctx); tx != nil {
		return tx.QueryRowContext(ctx, query, args...)
	}
	return r.db.r.QueryRowContext(ctx, query, args...)
}

// R is the read pool, for a read of one statement. Under a Snapshot's ctx
// it reads in that snapshot's transaction.
func (db *DB) R() Reader { return reader{db} }

// Read runs fn in one deferred read transaction on the pool, so every
// statement sees one snapshot; under a Snapshot's ctx it runs in that
// snapshot's transaction. fn must not write.
func (db *DB) Read(ctx context.Context, fn func(tx *sql.Tx) error) error {
	if tx := db.pinned(ctx); tx != nil {
		return fn(tx)
	}
	tx, err := db.r.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return fn(tx)
}

// Snapshot pins one deferred read transaction to the returned ctx: every
// store read made with it inside fn, in sqlite and inventory, uses that
// transaction, so a multi-method load sees one commit.
func (db *DB) Snapshot(ctx context.Context, fn func(ctx context.Context) error) error {
	if db.pinned(ctx) != nil {
		return fn(ctx)
	}
	tx, err := db.r.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return fn(context.WithValue(ctx, pinKey{db}, tx))
}

// Tx is a write transaction that records what it changed.
type Tx struct {
	*sql.Tx
	topics port.Topic
	units  []unit.Unit
	all    bool
}

// Touch records that the transaction changed rows of topic t for units; a
// store method calls it only for rows a statement changed. No units: a
// unit-less change.
func (tx *Tx) Touch(t port.Topic, units ...unit.Unit) {
	tx.topics |= t
	tx.units = append(tx.units, units...)
}

// TouchAll records a change to rows of topic t it does not enumerate.
func (tx *Tx) TouchAll(t port.Topic) {
	tx.topics |= t
	tx.all = true
}

// Write runs fn in one write transaction under db.mu. After COMMIT, when fn
// touched anything, it notes the log (still under db.mu, so log order is
// commit order) and returns that Change; otherwise the zero Change. An error
// from fn rolls back and notes nothing. A nested Write panics.
func (db *DB) Write(ctx context.Context, fn func(tx *Tx) error) (port.Change, error) {
	defer db.lock()()
	stx, err := db.w.BeginTx(ctx, nil)
	if err != nil {
		return port.Change{}, err
	}
	defer stx.Rollback()
	tx := &Tx{Tx: stx}
	if err := fn(tx); err != nil {
		return port.Change{}, err
	}
	if err := stx.Commit(); err != nil {
		return port.Change{}, err
	}
	switch {
	case tx.topics == 0:
		return port.Change{}, nil
	case tx.all:
		return db.log.NoteAll(tx.topics), nil
	default:
		return db.log.Note(tx.topics, tx.units...), nil
	}
}

// lock takes db.mu for the calling goroutine and returns its unlock; a
// goroutine that already holds it panics.
func (db *DB) lock() (unlock func()) {
	g := goid()
	if !db.mu.TryLock() {
		if db.holder.Load() == g {
			panic("sqlite: Write inside Write")
		}
		db.mu.Lock()
	}
	db.holder.Store(g)
	return func() {
		db.holder.Store(0)
		db.mu.Unlock()
	}
}

// runOnWriter runs each statement on the writer outside any transaction, under
// the write lock, reading every row it returns: incremental_vacuum frees
// pages only as far as it is stepped.
func (db *DB) runOnWriter(ctx context.Context, stmts ...string) error {
	defer db.lock()()
	for _, q := range stmts {
		rows, err := db.w.QueryContext(ctx, q)
		if err != nil {
			return fmt.Errorf("%s: %w", q, err)
		}
		for rows.Next() {
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return fmt.Errorf("%s: %w", q, err)
		}
	}
	return nil
}

var goroutinePrefix = []byte("goroutine ")

// goid is the calling goroutine's id, read from its stack header.
func goid() uint64 {
	var buf [64]byte
	b := buf[:runtime.Stack(buf[:], false)]
	b = bytes.TrimPrefix(b, goroutinePrefix)
	if i := bytes.IndexByte(b, ' '); i > 0 {
		b = b[:i]
	}
	n, _ := strconv.ParseUint(string(b), 10, 64)
	return n
}
