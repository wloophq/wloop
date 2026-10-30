package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

func openMetered(t testing.TB) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "meter.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func count(s *meter.Scope, res meter.Resource, op string, f metertest.Field) int64 {
	return metertest.Count(s, metertest.Limit{Res: res, Op: op, Field: f})
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); !ok(); {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestOpenMetersEveryHandle(t *testing.T) {
	t.Parallel()
	db := openMetered(t)
	for name, h := range map[string]*sql.DB{"writer": db.w, "reader": db.r} {
		if _, ok := h.Driver().(meteredDriver); !ok {
			t.Fatalf("the %s's driver is %T, want the counting driver", name, h.Driver())
		}
	}
	ctx, s := metertest.Start(t, context.Background())
	var one int
	if err := db.R().QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil {
		t.Fatal(err)
	}
	if count(s, meter.SQL, "", metertest.Calls) != 1 || count(s, meter.SQL, "query", metertest.Calls) != 1 {
		t.Fatalf("SELECT 1 added\n%s\nwant one SQL query", metertest.Report(s.Tally()))
	}
	if _, err := db.Write(ctx, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES ('k', 'v')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if count(s, meter.SQL, "exec", metertest.Calls) != 1 || count(s, meter.Tx, "write", metertest.Calls) != 1 {
		t.Fatalf("a Write added\n%s\nwant one SQL exec and one write Tx", metertest.Report(s.Tally()))
	}

	named, err := sql.Open(driverName, filepath.Join(t.TempDir(), "named.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer named.Close()
	if err := named.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil {
		t.Fatal(err)
	}
	if got := count(s, meter.SQL, "query", metertest.Calls); got != 2 {
		t.Fatalf("a handle opened by driver name added %d queries in all, want 2", got)
	}
}

func TestQuerySampleCarriesRows(t *testing.T) {
	t.Parallel()
	db := openMetered(t)
	ctx := context.Background()
	if _, err := db.w.ExecContext(ctx, `CREATE TABLE n (i INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.w.ExecContext(ctx, `WITH RECURSIVE c(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM c WHERE i < 100) INSERT INTO n SELECT i FROM c`); err != nil {
		t.Fatal(err)
	}
	ctx, s := metertest.Start(t, ctx)
	rows, err := db.r.QueryContext(ctx, "SELECT i\n\tFROM n")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if calls, rows := count(s, meter.SQL, "query", metertest.Calls), count(s, meter.SQL, "query", metertest.Rows); calls != 1 || rows != 100 {
		t.Fatalf("%d queries with %d rows, want 1 with 100", calls, rows)
	}
	if got := s.Samples()[0].Detail; got != "SELECT i FROM n" {
		t.Fatalf("Detail %q, want the statement with whitespace collapsed", got)
	}
}

func TestReadTxIsRead(t *testing.T) {
	t.Parallel()
	db := openMetered(t)
	ctx, s := metertest.Start(t, context.Background())
	tx, err := db.r.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM meta`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if count(s, meter.Tx, "", metertest.Calls) != 1 || count(s, meter.Tx, "read", metertest.Calls) != 1 {
		t.Fatalf("tally\n%s\nwant one read Tx", metertest.Report(s.Tally()))
	}
	if got := count(s, meter.SQL, "query", metertest.Calls); got != 1 {
		t.Fatalf("%d queries, want 1", got)
	}
}

func TestWriteTxIsWrite(t *testing.T) {
	t.Parallel()
	db := openMetered(t)
	ctx, s := metertest.Start(t, context.Background())
	tx, err := db.w.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES ('k', 'v')`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if count(s, meter.Tx, "", metertest.Calls) != 1 || count(s, meter.Tx, "write", metertest.Calls) != 1 {
		t.Fatalf("tally\n%s\nwant one write Tx", metertest.Report(s.Tally()))
	}
	if got := count(s, meter.SQL, "exec", metertest.Calls); got != 1 {
		t.Fatalf("%d execs, want 1", got)
	}

	tx, err = db.w.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.QueryContext(ctx, `SELECT 1`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got := count(s, meter.Tx, "read", metertest.Calls); got != 1 {
		t.Fatalf("a transaction after a write one counted %d reads, want 1", got)
	}
}

func TestPreparedStatementsAreMetered(t *testing.T) {
	t.Parallel()
	db := openMetered(t)
	ctx, s := metertest.Start(t, context.Background())
	tx, err := db.w.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO meta (key, value) VALUES (?, 'v')`)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"a", "b", "c"} {
		if _, err := stmt.ExecContext(ctx, k); err != nil {
			t.Fatal(err)
		}
	}
	stmt.Close()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := count(s, meter.SQL, "exec", metertest.Calls); got != 3 {
		t.Fatalf("%d execs, want 3", got)
	}
	if got := count(s, meter.Tx, "write", metertest.Calls); got != 1 {
		t.Fatalf("%d write Tx, want 1", got)
	}
}

func TestMeteredRowsKeepColumnTypes(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "types.db")
	db, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	bare, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer bare.Close()
	const q = `SELECT id, kind, actor, at FROM events`
	describe := func(h *sql.DB) []string {
		rows, err := h.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		cts, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, ct := range cts {
			length, lok := ct.Length()
			nullable, nok := ct.Nullable()
			p, sc, pok := ct.DecimalSize()
			out = append(out, fmt.Sprint(ct.Name(), ct.DatabaseTypeName(), ct.ScanType(), length, lok, nullable, nok, p, sc, pok))
		}
		return out
	}
	if got, want := describe(db.r), describe(bare); !reflect.DeepEqual(got, want) {
		t.Fatalf("metered column types %v, bare %v", got, want)
	}
}

func TestCanceledIsNotError(t *testing.T) {
	t.Parallel()
	db := openMetered(t)
	ctx, s := metertest.Start(t, context.Background())
	if _, err := db.w.ExecContext(ctx, `SELECT * FROM no_such_table`); err == nil {
		t.Fatal("a query of a missing table succeeded")
	}
	if got := count(s, meter.SQL, "exec", metertest.Errors); got != 1 {
		t.Fatalf("a failing statement counted %d errors, want 1", got)
	}

	cctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	_, err := db.w.ExecContext(cctx, `WITH RECURSIVE c(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM c) SELECT count(*) FROM c`)
	if err == nil {
		t.Fatal("an endless statement finished")
	}
	if calls, errs := count(s, meter.SQL, "exec", metertest.Calls), count(s, meter.SQL, "exec", metertest.Errors); calls != 2 || errs != 1 {
		t.Fatalf("%d execs with %d errors after a deadline, want 2 and still 1", calls, errs)
	}
}

func TestRowsClosedEarly(t *testing.T) {
	t.Parallel()
	db := openMetered(t)
	ctx, s := metertest.Start(t, context.Background())
	rows, err := db.r.QueryContext(ctx, `WITH RECURSIVE c(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM c WHERE i < 100) SELECT i FROM c`)
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		t.Fatal("no first row")
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	calls, rowsRead, errs := count(s, meter.SQL, "query", metertest.Calls), count(s, meter.SQL, "query", metertest.Rows), count(s, meter.SQL, "query", metertest.Errors)
	if calls != 1 || rowsRead != 1 || errs != 0 {
		t.Fatalf("%d queries, %d rows, %d errors; want 1 query with 1 row and no error", calls, rowsRead, errs)
	}
}

func TestCanceledRowsAreNotError(t *testing.T) {
	t.Parallel()
	db := openMetered(t)
	ctx, s := metertest.Start(t, context.Background())
	cctx, cancel := context.WithCancel(ctx)
	rows, err := db.r.QueryContext(cctx, `WITH RECURSIVE c(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM c WHERE i < 100) SELECT i FROM c`)
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		t.Fatal("no first row")
	}
	cancel()
	eventually(t, "the canceled rows' sample", func() bool { return count(s, meter.SQL, "query", metertest.Calls) == 1 })
	for rows.Next() {
	}
	rows.Close()
	if calls, errs := count(s, meter.SQL, "query", metertest.Calls), count(s, meter.SQL, "query", metertest.Errors); calls != 1 || errs != 0 {
		t.Fatalf("%d queries with %d errors after a cancel, want 1 with none", calls, errs)
	}
}

func TestCanceledTxIsNotError(t *testing.T) {
	t.Parallel()
	db := openMetered(t)
	ctx, s := metertest.Start(t, context.Background())
	cctx, cancel := context.WithCancel(ctx)
	tx, err := db.w.BeginTx(cctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(cctx, `INSERT INTO meta (key, value) VALUES ('k', 'v')`); err != nil {
		t.Fatal(err)
	}
	cancel()
	eventually(t, "the canceled transaction's sample", func() bool { return count(s, meter.Tx, "", metertest.Calls) == 1 })
	tx.Rollback()
	if calls, errs := count(s, meter.Tx, "write", metertest.Calls), count(s, meter.Tx, "", metertest.Errors); calls != 1 || errs != 0 {
		t.Fatalf("%d write Tx with %d errors after a cancel, want 1 with none", calls, errs)
	}
}

func BenchmarkPointQuery(b *testing.B) {
	path := filepath.Join(b.TempDir(), "bench.db")
	metered, err := Open(path, nil)
	if err != nil {
		b.Fatal(err)
	}
	defer metered.Close()
	if _, err := metered.w.Exec(`INSERT INTO meta (key, value) VALUES ('k', 'v')`); err != nil {
		b.Fatal(err)
	}
	bare, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=immediate")
	if err != nil {
		b.Fatal(err)
	}
	defer bare.Close()
	bare.SetMaxOpenConns(1)

	ctx := meter.WithRegistry(context.Background(), meter.NewRegistry(time.Now))
	ctx, _ = meter.Start(ctx, meter.Job("bench"))
	for _, c := range []struct {
		name string
		db   *sql.DB
		ctx  context.Context
	}{
		{"bare", bare, context.Background()},
		{"metered", metered.r, ctx},
	} {
		b.Run(c.name, func(b *testing.B) {
			var v string
			for b.Loop() {
				if err := c.db.QueryRowContext(c.ctx, `SELECT value FROM meta WHERE key = ?`, "k").Scan(&v); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestAgentHomeOneQuery(t *testing.T) {
	t.Parallel()
	db := openMetered(t)
	ctx, s := metertest.Start(t, context.Background())
	r := NewRounds(db)
	ref := pr.Ref{Owner: "acme", Repo: "api", Number: 7}
	st := round.New()
	st.SetAgentHome(ref.String(), "/home/me/.claude")
	if err := r.Save(ctx, st); err != nil {
		t.Fatal(err)
	}
	before := count(s, meter.SQL, "query", metertest.Calls)
	home, err := r.AgentHome(ctx, ref)
	if err != nil || home != "/home/me/.claude" {
		t.Fatalf("AgentHome = %q, %v, want the saved home", home, err)
	}
	if got := count(s, meter.SQL, "query", metertest.Calls) - before; got != 1 {
		t.Fatalf("AgentHome ran %d queries, want 1\n%s", got, metertest.Report(s.Tally()))
	}
	other, err := r.AgentHome(ctx, pr.Ref{Owner: "acme", Repo: "api", Number: 8})
	if err != nil || other != "" {
		t.Fatalf("AgentHome of an unknown PR = %q, %v, want empty", other, err)
	}
}
