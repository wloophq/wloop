package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/pr"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

func TestWriterPragmas(t *testing.T) {
	db, _ := openTemp(t)
	want := map[string]string{
		"journal_mode":       "wal",
		"synchronous":        "1",
		"temp_store":         "2",
		"cache_size":         "-8000",
		"mmap_size":          "67108864",
		"journal_size_limit": "8388608",
		"foreign_keys":       "1",
		"busy_timeout":       "5000",
		"query_only":         "0",
	}
	for name, h := range map[string]*sql.DB{"writer": db.w, "reader": db.r} {
		for p, v := range want {
			if name == "reader" && p == "query_only" {
				v = "1"
			}
			var got string
			if err := h.QueryRow(`PRAGMA ` + p).Scan(&got); err != nil || got != v {
				t.Errorf("%s: PRAGMA %s = %q %v, want %q", name, p, got, err, v)
			}
		}
	}
	if got := db.r.Stats().MaxOpenConnections; got != ReadConns {
		t.Errorf("the read pool holds %d connections, want %d", got, ReadConns)
	}
	if got := db.w.Stats().MaxOpenConnections; got != 1 {
		t.Errorf("the writer holds %d connections, want 1", got)
	}
}

func TestReaderIsQueryOnly(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	err := db.Read(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES ('k', 'v')`)
		return err
	})
	var se *sqlite.Error
	if !errors.As(err, &se) || se.Code()&0xff != sqlite3.SQLITE_READONLY {
		t.Fatalf("an INSERT on the pool: %v, want SQLITE_READONLY", err)
	}
	var n int
	if err := db.R().QueryRowContext(ctx, `SELECT count(*) FROM meta WHERE key = 'k'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d rows %v", n, err)
	}
}

func TestReadDoesNotBlockWrite(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	held, release := make(chan struct{}), make(chan struct{})
	go db.Read(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM meta`).Scan(&n); err != nil {
			t.Error(err)
		}
		close(held)
		<-release
		return nil
	})
	defer close(release)
	<-held
	wctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if _, err := db.Write(wctx, func(tx *Tx) error {
		_, err := tx.ExecContext(wctx, `INSERT INTO meta (key, value) VALUES ('k', 'v')`)
		return err
	}); err != nil {
		t.Fatalf("a Write under an open Read: %v", err)
	}
}

func TestWriteDoesNotBlockRead(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	held, release, done := make(chan struct{}), make(chan struct{}), make(chan error)
	go func() {
		_, err := db.Write(ctx, func(tx *Tx) error {
			if _, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES ('k', 'v')`); err != nil {
				return err
			}
			close(held)
			<-release
			return nil
		})
		done <- err
	}()
	<-held
	rctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for range ReadConns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var n int
			if err := db.R().QueryRowContext(rctx, `SELECT count(*) FROM meta WHERE key = 'k'`).Scan(&n); err != nil || n != 0 {
				t.Errorf("a read under an open Write: %d rows %v", n, err)
			}
		}()
	}
	wg.Wait()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestReadSeesOneSnapshot(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	var first, second int
	err := db.Read(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM meta`).Scan(&first); err != nil {
			return err
		}
		if _, err := execW(db, `INSERT INTO meta (key, value) VALUES ('k', 'v')`); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT count(*) FROM meta`).Scan(&second)
	})
	if err != nil || first != second {
		t.Fatalf("one Read saw %d then %d rows (%v)", first, second, err)
	}
}

func TestSnapshotPinsOneTransaction(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	count := func(ctx context.Context) int {
		var n int
		if err := db.R().QueryRowContext(ctx, `SELECT count(*) FROM meta`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	var before, viaR, viaRead, viaStore int
	err := db.Snapshot(ctx, func(ctx context.Context) error {
		before = count(ctx)
		if _, err := execW(db, `INSERT INTO meta (key, value) VALUES ('k', 'v')`); err != nil {
			return err
		}
		viaR = count(ctx)
		if err := db.Read(ctx, func(tx *sql.Tx) error {
			return tx.QueryRowContext(ctx, `SELECT count(*) FROM meta`).Scan(&viaRead)
		}); err != nil {
			return err
		}
		return db.Snapshot(ctx, func(ctx context.Context) error {
			viaStore = count(ctx)
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if viaR != before || viaRead != before || viaStore != before {
		t.Fatalf("inside one Snapshot: %d rows, then R %d, Read %d, nested Snapshot %d", before, viaR, viaRead, viaStore)
	}
	if after := count(ctx); after != before+1 {
		t.Fatalf("after the Snapshot %d rows, want %d", after, before+1)
	}
}

func TestSnapshotPinsOnlyItsOwnStore(t *testing.T) {
	a, _ := openTemp(t)
	b, _ := openTemp(t)
	ctx := context.Background()
	if _, err := execW(b, `INSERT INTO meta (key, value) VALUES ('only-b', 'v')`); err != nil {
		t.Fatal(err)
	}
	err := a.Snapshot(ctx, func(ctx context.Context) error {
		var n int
		if err := b.R().QueryRowContext(ctx, `SELECT count(*) FROM meta WHERE key = 'only-b'`).Scan(&n); err != nil || n != 1 {
			t.Errorf("b under a's snapshot read %d rows (%v), want b's own 1", n, err)
		}
		return b.Read(ctx, func(tx *sql.Tx) error {
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM meta WHERE key = 'only-b'`).Scan(&n); err != nil || n != 1 {
				t.Errorf("b.Read under a's snapshot read %d rows (%v), want 1", n, err)
			}
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCloseNamesLeakedConnections(t *testing.T) {
	if testing.Short() {
		t.Skip("waits closeWait")
	}
	db, err := Open(filepath.Join(t.TempDir(), "leak.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := db.R().QueryContext(context.Background(), `SELECT key FROM meta`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if err := db.Close(); err == nil || !strings.Contains(err.Error(), "1 connections still open") {
		t.Fatalf("Close with a leaked Rows: %v", err)
	}
}

func TestWriteNotesWhatItTouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.db")
	log := change.NewLog(change.DefaultWindow, nil)
	db, err := Open(path, log)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	u := pr.Ref{Owner: "acme", Repo: "api", Number: 7}.Unit()
	got, err := db.Write(ctx, func(tx *Tx) error {
		tx.Touch(port.TopicPRs, u)
		tx.Touch(port.TopicFollow)
		return nil
	})
	if err != nil || got.Gen != 1 || got.Topics != port.TopicPRs|port.TopicFollow || len(got.Units) != 1 || got.Units[0] != u {
		t.Fatalf("%+v %v", got, err)
	}
	if got, err := db.Write(ctx, func(tx *Tx) error { return nil }); err != nil || !got.Empty() || got.Gen != 0 {
		t.Fatalf("an untouched Write returned %+v %v", got, err)
	}
	if got, err := db.Write(ctx, func(tx *Tx) error {
		tx.Touch(port.TopicMeta, u)
		return errors.New("rolled back")
	}); err == nil || got.Gen != 0 {
		t.Fatalf("a failed Write returned %+v %v", got, err)
	}
	if got, err := db.Write(ctx, func(tx *Tx) error {
		tx.Touch(port.TopicTurns, u)
		tx.TouchAll(port.TopicLoopEvents)
		return nil
	}); err != nil || !got.All || got.Topics != port.TopicTurns|port.TopicLoopEvents || got.Gen != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	if log.Gen() != 2 {
		t.Fatalf("the log is at %d, want 2", log.Gen())
	}
}

func TestWriteInsideWritePanics(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	defer func() {
		if r := recover(); r != "sqlite: Write inside Write" {
			t.Fatalf("recovered %v", r)
		}
		if _, err := db.Write(ctx, func(*Tx) error { return nil }); err != nil {
			t.Fatalf("a Write after the panic: %v", err)
		}
	}()
	db.Write(ctx, func(*Tx) error {
		db.Write(ctx, func(*Tx) error { return nil })
		return nil
	})
}

// TestNoWriterOutsideWrite scans the two store packages: only db.go opens
// or begins on a handle, and no function holding a transaction reads the
// pool, directly or through a store method.
func TestNoWriterOutsideWrite(t *testing.T) {
	for _, dir := range []string{".", "../inventory"} {
		for _, problem := range writerProblems(t, dir) {
			t.Error(problem)
		}
	}
}

func TestNoWriterOutsideWriteFindsViolations(t *testing.T) {
	dir := t.TempDir()
	src := `package bad

func (s *Store) Sessions(ctx context.Context) ([]int, error) { return nil, nil }

func (s *Store) A(ctx context.Context) {
	s.db.w.ExecContext(ctx, "DELETE FROM meta")
	s.db.BeginTx(ctx, nil)
	sql.Open("sqlite", "x")
	s.db.Write(ctx, func(tx *sqlite.Tx) error {
		return s.db.Read(ctx, nil)
	})
}

func (s *Store) probe(ctx context.Context) error {
	return s.write(ctx, nil, func(tx *Tx) error {
		_, err := s.Sessions(ctx)
		var n int
		_ = s.db.R().QueryRowContext(ctx, "SELECT 1").Scan(&n)
		return err
	})
}

func probeHelper(ctx context.Context, db *DB, tx *Tx) { db.R().QueryRowContext(ctx, "SELECT 1") }

func readHelper(ctx context.Context, s *Store, tx *sql.Tx) { s.Snapshot(ctx, nil) }
`
	if err := os.WriteFile(filepath.Join(dir, "bad.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(writerProblems(t, dir), "\n")
	for _, want := range []string{
		"bad.go:6:2: the writer handle",
		"bad.go:7:2: BeginTx",
		"bad.go:8:2: sql.Open",
		"bad.go:10:10: Read inside a transaction",
		"bad.go:16:13: store method Sessions inside a transaction",
		"bad.go:18:7: R inside a transaction",
		"bad.go:23:57: R inside a transaction",
		"bad.go:25:62: Snapshot inside a transaction",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("no problem %q in\n%s", want, got)
		}
	}
}

func writerProblems(t *testing.T, dir string) []string {
	t.Helper()
	fset := token.NewFileSet()
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]*ast.File{}
	methods := map[string]bool{}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files[filepath.Base(path)] = f
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv != nil && fd.Name.IsExported() {
				methods[fd.Name.Name] = true
			}
		}
	}
	var out []string
	for name, f := range files {
		if name == "db.go" {
			continue
		}
		report := func(n ast.Node, what string) { out = append(out, fset.Position(n.Pos()).String()+": "+what) }
		opens := name == "sqlite.go" || name == "meter.go"
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.SelectorExpr:
				if n.Sel.Name == "w" {
					report(n, "the writer handle outside db.go")
				}
				if x, ok := n.X.(*ast.Ident); ok && x.Name == "sql" && (n.Sel.Name == "Open" || n.Sel.Name == "OpenDB") && !opens {
					report(n, "sql."+n.Sel.Name+" outside the files that open the store")
				}
			case *ast.CallExpr:
				if sel, ok := n.Fun.(*ast.SelectorExpr); ok && (sel.Sel.Name == "BeginTx" || sel.Sel.Name == "Begin") && !opens {
					report(n, sel.Sel.Name+" outside db.go")
				}
			}
			return true
		})
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			stores := map[string]bool{}
			if fd.Recv != nil {
				for _, n := range fd.Recv.List[0].Names {
					stores[n.Name] = true
				}
			}
			for _, p := range fd.Type.Params.List {
				if isStoreType(p.Type) {
					for _, n := range p.Names {
						stores[n.Name] = true
					}
				}
			}
			if txParams(fd.Type) != nil {
				out = append(out, poolInTx(fset, fd.Body, stores, methods)...)
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				if lit, ok := n.(*ast.FuncLit); ok && txParams(lit.Type) != nil {
					out = append(out, poolInTx(fset, lit.Body, stores, methods)...)
					return false
				}
				return true
			})
		}
	}
	return out
}

// txParams names the transaction parameters of a function: *Tx, *sqlite.Tx
// or *sql.Tx.
func txParams(ft *ast.FuncType) map[string]bool {
	var names map[string]bool
	for _, p := range ft.Params.List {
		star, ok := p.Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		tx := false
		switch x := star.X.(type) {
		case *ast.Ident:
			tx = x.Name == "Tx"
		case *ast.SelectorExpr:
			tx = x.Sel.Name == "Tx"
		}
		if tx {
			if names == nil {
				names = map[string]bool{}
			}
			for _, n := range p.Names {
				names[n.Name] = true
			}
		}
	}
	return names
}

func isStoreType(e ast.Expr) bool {
	star, ok := e.(*ast.StarExpr)
	if !ok {
		return false
	}
	switch x := star.X.(type) {
	case *ast.Ident:
		return x.Name == "DB" || x.Name == "Store"
	case *ast.SelectorExpr:
		return x.Sel.Name == "DB" || x.Sel.Name == "Store"
	}
	return false
}

// poolInTx reports, inside a function holding a transaction, a call of R,
// Read, Snapshot or Write, and a store method called on the store or its db.
func poolInTx(fset *token.FileSet, body *ast.BlockStmt, stores, methods map[string]bool) []string {
	var out []string
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		at := fset.Position(call.Pos()).String()
		switch sel.Sel.Name {
		case "R", "Read", "Snapshot", "Write":
			out = append(out, at+": "+sel.Sel.Name+" inside a transaction: read on the tx")
			return true
		}
		if methods[sel.Sel.Name] && onStore(sel.X, stores) {
			out = append(out, at+": store method "+sel.Sel.Name+" inside a transaction: read on the tx")
		}
		return true
	})
	return out
}

func onStore(x ast.Expr, stores map[string]bool) bool {
	if s, ok := x.(*ast.SelectorExpr); ok && s.Sel.Name == "db" {
		x = s.X
	}
	id, ok := x.(*ast.Ident)
	return ok && stores[id.Name]
}
