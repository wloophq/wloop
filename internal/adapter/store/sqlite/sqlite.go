// Package sqlite is the loop's one store: a SQLite database with one versioned
// schema that every repository shares.
package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"
)

//go:embed schema.sql
var schema string

// SchemaVersion is the PRAGMA user_version of a store this release creates
// and migrates to.
const SchemaVersion = 1

const TimeLayout = time.RFC3339Nano

// compatibleKey is the meta row holding the lowest schema version a release
// must read to use the store; a store without it needs its own version.
const compatibleKey = "compatible_version"

var ErrSchema = errors.New("the store is not at this release's schema version")

// State is how a store's version stands against this release.
type State int

const (
	// StateCurrent is a store at SchemaVersion, or no store yet.
	StateCurrent State = iota
	// StateOlder is a store Migrate takes to SchemaVersion.
	StateOlder
	// StateReadable is a newer store whose changes this release can still use.
	StateReadable
	// StateNewer is a newer store this release must not open.
	StateNewer
	// StateUnknown is a store at no version a release writes.
	StateUnknown
)

// Status is a store's schema version as read from its file.
type Status struct {
	Version uint32
	// Compatible is the lowest version a release must read to use the store.
	Compatible uint32
	// Empty is an absent store, or one with no table yet.
	Empty bool
}

// State places s against this release.
func (s Status) State() State { return s.stateFor(SchemaVersion) }

func (s Status) stateFor(reads uint32) State {
	switch {
	case s.Empty || s.Version == reads:
		return StateCurrent
	case s.Version == 0:
		return StateUnknown
	case s.Version < reads:
		return StateOlder
	case s.Compatible <= reads:
		return StateReadable
	default:
		return StateNewer
	}
}

// Opens says this release opens a store in s without migrating it.
func (s Status) Opens() bool {
	st := s.State()
	return st == StateCurrent || st == StateReadable
}

// SchemaError is a store at a version this release cannot open as it is.
type SchemaError struct {
	Path   string
	Status Status
	reads  uint32
}

func (e SchemaError) Error() string {
	v := e.Status.Version
	switch e.Status.stateFor(e.reads) {
	case StateOlder:
		return fmt.Sprintf("the store at %s is at schema version %d; this release migrates it to version %d before opening it", e.Path, v, e.reads)
	case StateNewer:
		return fmt.Sprintf("the store at %s is at schema version %d, newer than this release reads (%d)", e.Path, v, e.reads)
	default:
		return fmt.Sprintf("the store at %s is at schema version %d; this release reads versions 1 to %d only", e.Path, v, e.reads)
	}
}

func (e SchemaError) Is(target error) bool { return target == ErrSchema }

func (e SchemaError) State() State { return e.Status.stateFor(e.reads) }

func (set schemaSet) refuse(path string, s Status) error {
	switch s.stateFor(set.version) {
	case StateCurrent, StateReadable:
		return nil
	}
	return SchemaError{path, s, set.version}
}

// createIfEmpty writes the schema into a store with no table, before the
// store turns to WAL, so the file's header carries the version for Check.
// auto_vacuum takes effect only on a file with no table yet, so it is set on
// the connection, outside the transaction.
func (set schemaSet) createIfEmpty(path string) error {
	c, err := connector(path + "?_pragma=auto_vacuum(INCREMENTAL)&_pragma=busy_timeout(5000)&_txlock=immediate")
	if err != nil {
		return err
	}
	db := sql.OpenDB(c)
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow(`SELECT count(*) FROM sqlite_master`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if _, err := tx.Exec(set.schema); err != nil {
		return fmt.Errorf("creating the store: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO meta (key, value) VALUES ('timings_since', ?), (?, ?)`,
		eventTime(time.Now()), compatibleKey, strconv.FormatUint(uint64(set.compatibleAt(set.version)), 10)); err != nil {
		return err
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, set.version)); err != nil {
		return err
	}
	return tx.Commit()
}

// Check refuses a store at path that this release cannot open as it is,
// reading its file without opening it for writing. An absent or empty file
// passes: Open creates it.
func Check(path string) error {
	s, err := Inspect(path)
	if err != nil {
		return err
	}
	return current.refuse(path, s)
}

// Inspect reads the schema version of the store at path from its header;
// for a store newer than this release it also reads the compatible version,
// opening the store for reads.
func Inspect(path string) (Status, error) { return current.inspect(path) }

func (set schemaSet) inspect(path string) (Status, error) {
	v, ok, err := headerVersion(path)
	if err != nil || !ok {
		return Status{Empty: !ok}, err
	}
	s := Status{Version: v, Compatible: v}
	if v == set.version {
		return s, nil
	}
	// The header lags a version change still in the WAL.
	db, err := handle(path+"?_pragma=query_only(1)&_pragma=busy_timeout(5000)", 1)
	if err != nil {
		return s, err
	}
	defer db.Close()
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&s.Version); err != nil {
		return s, err
	}
	s.Compatible = s.Version
	if s.Version > set.version {
		s.Compatible, err = readCompatible(context.Background(), db, s.Version)
	}
	return s, err
}

// headerVersion is the user_version in the header of the file at path; ok
// is false for an absent file or one too short to hold a header.
func headerVersion(path string) (v uint32, ok bool, err error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	defer f.Close()
	header := make([]byte, 100)
	if _, err := io.ReadFull(f, header); err != nil {
		return 0, false, nil
	}
	return binary.BigEndian.Uint32(header[60:64]), true, nil
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// readCompatible is the store's compatible version, v when it records none.
func readCompatible(ctx context.Context, q queryer, v uint32) (uint32, error) {
	var raw string
	err := q.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, compatibleKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return v, nil
	}
	if err != nil {
		return 0, err
	}
	c, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("the store's %s is %q: %w", compatibleKey, raw, err)
	}
	return uint32(c), nil
}
