package store_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/wloophq/wloop/pkg/plugin/store"
)

func exec(q string) func(context.Context, *sql.Tx) error {
	return func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, q)
		return err
	}
}

func TestAStoreMigratesOnceAndImportsOnce(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	exported, err := sql.Open("sqlite", filepath.Join(dir, store.ImportFile))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exported.ExecContext(ctx, "CREATE TABLE things(name TEXT); INSERT INTO things VALUES ('ana'), ('bruno')"); err != nil {
		t.Fatal(err)
	}
	exported.Close()
	imports := 0
	opts := store.Options{
		Migrations: []store.Migration{{Version: 1, Up: exec("CREATE TABLE things(name TEXT)")}},
		Import: func(ctx context.Context, tx *sql.Tx) error {
			imports++
			return exec("INSERT INTO things SELECT name FROM "+store.ImportSchema+".things")(ctx, tx)
		},
	}
	for range 2 {
		db, err := store.Open(ctx, dir, opts)
		if err != nil {
			t.Fatal(err)
		}
		var n int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM things").Scan(&n); err != nil || n != 2 {
			t.Errorf("things %d, %v", n, err)
		}
		db.Close()
	}
	if imports != 1 {
		t.Errorf("imported %d times", imports)
	}
	if _, err := os.Stat(filepath.Join(dir, store.Imported)); err != nil {
		t.Errorf("the import file was not marked done: %v", err)
	}
	if fi, err := os.Stat(filepath.Join(dir, store.File)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("store mode: %v %v", fi, err)
	}
}

func TestAFailedImportLeavesTheFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, store.ImportFile), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	failed := errors.New("no")
	_, err := store.Open(t.Context(), dir, store.Options{Import: func(context.Context, *sql.Tx) error { return failed }})
	if !errors.Is(err, failed) {
		t.Fatalf("err %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, store.ImportFile)); err != nil {
		t.Errorf("the import file moved: %v", err)
	}
}

func TestACrashBeforeTheRenameDoesNotImportTwice(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	exported, err := sql.Open("sqlite", filepath.Join(dir, store.ImportFile))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exported.ExecContext(ctx, "CREATE TABLE things(name TEXT); INSERT INTO things VALUES ('ana')"); err != nil {
		t.Fatal(err)
	}
	exported.Close()
	done := filepath.Join(dir, store.Imported)
	if err := os.Mkdir(done, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(done, "x"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	imports := 0
	opts := store.Options{
		Migrations: []store.Migration{{Version: 1, Up: exec("CREATE TABLE things(name TEXT)")}},
		Import: func(ctx context.Context, tx *sql.Tx) error {
			imports++
			return exec("INSERT INTO things SELECT name FROM "+store.ImportSchema+".things")(ctx, tx)
		},
	}
	if _, err := store.Open(ctx, dir, opts); err == nil {
		t.Fatal("the planted crash: the rename was not refused")
	}
	if err := os.RemoveAll(done); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, dir, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM things").Scan(&n); err != nil || n != 1 || imports != 1 {
		t.Errorf("things %d, imports %d, %v", n, imports, err)
	}
	if _, err := os.Stat(filepath.Join(dir, store.ImportFile)); !os.IsNotExist(err) {
		t.Errorf("the import file is left: %v", err)
	}
}

func TestTwoHandlesOfAStoreWriteWithoutABusyError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	opts := store.Options{Migrations: []store.Migration{{Version: 1, Up: exec(`CREATE TABLE counter (n INTEGER NOT NULL); INSERT INTO counter VALUES (0)`)}}}
	var dbs []*sql.DB
	for range 2 {
		db, err := store.Open(ctx, dir, opts)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		dbs = append(dbs, db)
	}
	const each = 50
	errs := make(chan error, len(dbs))
	for _, db := range dbs {
		go func() {
			for range each {
				err := func() error {
					tx, err := db.BeginTx(ctx, nil)
					if err != nil {
						return err
					}
					defer tx.Rollback()
					var n int
					if err := tx.QueryRowContext(ctx, `SELECT n FROM counter`).Scan(&n); err != nil {
						return err
					}
					if _, err := tx.ExecContext(ctx, `UPDATE counter SET n = ?`, n+1); err != nil {
						return err
					}
					return tx.Commit()
				}()
				if err != nil {
					errs <- err
					return
				}
			}
			errs <- nil
		}()
	}
	for range dbs {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := dbs[0].QueryRowContext(ctx, `SELECT n FROM counter`).Scan(&n); err != nil || n != len(dbs)*each {
		t.Fatalf("counter %d (%v), want %d: a read-then-write lost an update or failed", n, err, len(dbs)*each)
	}
}
