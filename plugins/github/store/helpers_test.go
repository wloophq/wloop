package store

import (
	"context"
	"database/sql"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/plugins/github/pr"
)

var (
	ref    = pr.Ref{Owner: "acme", Repo: "api", Number: 1}
	noteAt = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
)

func open(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func upsert(t *testing.T, db *DB, p pr.PR) {
	t.Helper()
	if err := db.UpsertPR(context.Background(), p); err != nil {
		t.Fatal(err)
	}
}

func openPR(r pr.Ref, reason string) pr.PR {
	return pr.PR{Ref: r, State: pr.StateOpen, Title: "t", Reasons: []string{reason}, LastSeenAt: noteAt}
}

func exec(t *testing.T, db *DB, q string, args ...any) {
	t.Helper()
	if _, err := db.db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// changes records every change the store's commits are heard with.
type changes struct {
	mu  sync.Mutex
	all []Change
}

func listen(db *DB) *changes {
	c := &changes{}
	db.OnChange(func(ch Change) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.all = append(c.all, ch)
	})
	return c
}

// during is what fn's commits were heard with.
func (c *changes) during(t *testing.T, fn func() error) []Change {
	t.Helper()
	c.mu.Lock()
	n := len(c.all)
	c.mu.Unlock()
	if err := fn(); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.all[n:])
}

// rowWrites counts the rows of table a statement rewrote since it was
// called, through triggers.
func rowWrites(t *testing.T, db *DB, table string) func() int {
	t.Helper()
	exec(t, db, `CREATE TABLE IF NOT EXISTS test_writes (tbl TEXT NOT NULL)`)
	exec(t, db, `DELETE FROM test_writes WHERE tbl = ?`, table)
	for _, op := range []string{"INSERT", "UPDATE"} {
		exec(t, db, `CREATE TRIGGER IF NOT EXISTS count_`+table+`_`+op+` AFTER `+op+` ON `+table+` BEGIN INSERT INTO test_writes VALUES ('`+table+`'); END`)
	}
	return func() int {
		var n int
		if err := db.db.QueryRow(`SELECT count(*) FROM test_writes WHERE tbl = ?`, table).Scan(&n); err != nil && err != sql.ErrNoRows {
			t.Fatal(err)
		}
		return n
	}
}
