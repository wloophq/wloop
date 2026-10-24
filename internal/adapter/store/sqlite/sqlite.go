// Package sqlite is the loop's one store: a SQLite database with one versioned
// schema that every repository shares.
package sqlite

import (
	"database/sql"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

//go:embed schema.sql
var schema string

// SchemaVersion is the PRAGMA user_version of a store this release creates
// and reads.
const SchemaVersion = 1

const TimeLayout = time.RFC3339Nano

var ErrSchema = errors.New("the store is not at this release's schema version")

type schemaError struct {
	path    string
	version uint32
}

func (e schemaError) Error() string {
	return fmt.Sprintf("the store at %s is at schema version %d; this release reads version %d only", e.path, e.version, SchemaVersion)
}

func (e schemaError) Is(target error) bool { return target == ErrSchema }

func versionError(path string, v uint32) error {
	if v == SchemaVersion {
		return nil
	}
	return schemaError{path, v}
}

// createIfEmpty writes the schema into a store with no table, before the
// store turns to WAL, so the file's header carries the version for Check.
// auto_vacuum takes effect only on a file with no table yet, so it is set on
// the connection, outside the transaction.
func createIfEmpty(path string) error {
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
	if _, err := tx.Exec(schema); err != nil {
		return fmt.Errorf("creating the store: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO meta (key, value) VALUES ('timings_since', ?)`, eventTime(time.Now())); err != nil {
		return err
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, SchemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

// Check reads the version from the header of the store at path without
// opening it, for a process that is not the store's writer. An absent or
// empty file passes: Open creates it.
func Check(path string) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	header := make([]byte, 100)
	if _, err := io.ReadFull(f, header); err != nil {
		return nil
	}
	return versionError(path, binary.BigEndian.Uint32(header[60:64]))
}
