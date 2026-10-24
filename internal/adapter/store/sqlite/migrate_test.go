package sqlite

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// v1Store is a store at schema version 1 with one row in every table.
func v1Store(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state", "store.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := createIfEmpty(path); err != nil {
		t.Fatal(err)
	}
	db := rawOpen(t, path)
	defer db.Close()
	for _, q := range []string{`PRAGMA foreign_keys = OFF`, `PRAGMA ignore_check_constraints = ON`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range tables(t, db) {
		cols, vals := rowFor(t, db, table)
		q := fmt.Sprintf(`INSERT OR REPLACE INTO %q (%s) VALUES (%s)`, table, strings.Join(cols, ", "), strings.Join(vals, ", "))
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	return path
}

func rawOpen(t *testing.T, path string) *sql.DB {
	t.Helper()
	c, err := connector(path + "?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(c)
	db.SetMaxOpenConns(1)
	return db
}

func tables(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

// rowFor is a value for every column of table, of its declared type.
func rowFor(t *testing.T, db *sql.DB, table string) (cols, vals []string) {
	t.Helper()
	rows, err := db.Query(fmt.Sprintf(`PRAGMA table_info(%q)`, table))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var def sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &def, &pk); err != nil {
			t.Fatal(err)
		}
		cols = append(cols, fmt.Sprintf("%q", name))
		switch {
		case strings.Contains(strings.ToUpper(typ), "INT"):
			vals = append(vals, "1")
		case strings.Contains(strings.ToUpper(typ), "REAL"):
			vals = append(vals, "1.5")
		case strings.Contains(strings.ToUpper(typ), "BLOB"):
			vals = append(vals, "x'00'")
		default:
			vals = append(vals, "'x'")
		}
	}
	return cols, vals
}

func counts(t *testing.T, db *sql.DB) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, table := range tables(t, db) {
		var n int
		if err := db.QueryRow(fmt.Sprintf(`SELECT count(*) FROM %q`, table)).Scan(&n); err != nil {
			t.Fatal(err)
		}
		out[table] = n
	}
	return out
}

func headerVersion(t *testing.T, path string) uint32 {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return binary.BigEndian.Uint32(b[60:64])
}

func TestAV1StoreMigratesKeepingEveryRowAndABackup(t *testing.T) {
	path := v1Store(t)
	raw := rawOpen(t, path)
	before := counts(t, raw)
	raw.Close()
	if err := Check(path); err != nil {
		t.Fatalf("Check refuses an older store the daemon migrates: %v", err)
	}
	db, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	raw = rawOpen(t, path)
	defer raw.Close()
	if after := counts(t, raw); !mapsEqual(before, after) {
		t.Errorf("counts changed:\nbefore %v\nafter  %v", before, after)
	}
	for _, n := range before {
		if n == 0 {
			t.Fatalf("the v1 store has a table without its row: %v", before)
		}
	}
	if v := headerVersion(t, path); v != SchemaVersion {
		t.Errorf("header version %d, want %d", v, SchemaVersion)
	}
	bak := rawOpen(t, path+".v1.bak")
	defer bak.Close()
	var v int
	if err := bak.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != 1 {
		t.Fatalf("backup version %d %v", v, err)
	}
	if got := counts(t, bak); !mapsEqual(before, got) {
		t.Errorf("backup counts %v", got)
	}
}

func TestASecondBackupKeepsTheFirst(t *testing.T) {
	path := v1Store(t)
	if err := os.WriteFile(path+".v1.bak", []byte("an older backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := openErr(path); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path + ".v1.bak"); string(b) != "an older backup" {
		t.Error("the first backup was overwritten")
	}
	m, _ := filepath.Glob(path + ".v1.*.bak")
	if len(m) != 1 {
		t.Errorf("backups %v", m)
	}
}

func TestANewStoreIsAtTheCurrentVersionWithoutABackup(t *testing.T) {
	db, path := openTemp(t)
	var v int
	if err := db.R().QueryRowContext(context.Background(), `PRAGMA user_version`).Scan(&v); err != nil || v != SchemaVersion {
		t.Fatalf("user_version %d %v", v, err)
	}
	if m, _ := filepath.Glob(path + ".v*"); len(m) > 0 {
		t.Errorf("backups of a new store: %v", m)
	}
}

func TestANewerStoreIsRefused(t *testing.T) {
	for _, v := range []int{0, SchemaVersion + 1} {
		db, path := openTemp(t)
		for _, q := range []string{fmt.Sprintf(`PRAGMA user_version = %d`, v), `PRAGMA wal_checkpoint(TRUNCATE)`} {
			if _, err := db.w.Exec(q); err != nil {
				t.Fatal(err)
			}
		}
		db.Close()
		for name, err := range map[string]error{"Open": openErr(path), "Check": Check(path)} {
			if !errors.Is(err, ErrSchema) {
				t.Errorf("version %d, %s: %v", v, name, err)
			}
		}
	}
}

func mapsEqual(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

func TestVersion2ShapesDraftsFromTheirKind(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	if _, err := createIfEmpty(path); err != nil {
		t.Fatal(err)
	}
	raw := rawOpen(t, path)
	for _, d := range []struct{ unit, kind, state string }{
		{"C1/1", "nudge", "proposed"}, {"C1/2", "nudge", "sent"}, {"C1/3", "close", "failed"}, {"C1/4", "close", "sent"},
		{"C1/5", "react", "proposed"}, {"C1/6", "comment", "approved"}, {"C1/7", "reply", "proposed"}, {"C1/8", "ask_owner", "proposed"},
	} {
		if _, err := raw.Exec(`INSERT INTO drafts (provider, unit_key, kind, origin_kind, state, created_at, updated_at) VALUES ('slack', ?, ?, 'auto', ?, 'x', 'x')`, d.unit, d.kind, d.state); err != nil {
			t.Fatal(err)
		}
	}
	for _, u := range []string{"C1/4", "C1/3"} {
		if _, err := raw.Exec(`INSERT INTO extraction_cursors (provider, unit_key, at) VALUES ('slack', ?, 'x')`, u); err != nil {
			t.Fatal(err)
		}
	}
	raw.Close()
	if err := openErr(path); err != nil {
		t.Fatal(err)
	}
	raw = rawOpen(t, path)
	defer raw.Close()
	rows, err := raw.Query(`SELECT unit_key, purpose, delivery, effects FROM drafts ORDER BY unit_key`)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var u, p, d, e string
		if err := rows.Scan(&u, &p, &d, &e); err != nil {
			t.Fatal(err)
		}
		got = append(got, strings.Join([]string{u, p, d, e}, " "))
	}
	rows.Close()
	want := []string{
		`C1/1 nudge message ["entry.nudged"]`, `C1/2 nudge message []`, `C1/3 close message ["unit.closed"]`, `C1/4 close message []`,
		`C1/5 react reaction []`, `C1/6 comment comment []`, `C1/7 reply message []`, `C1/8 ask_owner message []`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s", strings.Join(got, "\n"))
	}
	var closed string
	if err := raw.QueryRow(`SELECT group_concat(unit_key) FROM extraction_cursors WHERE closure_posted = 1`).Scan(&closed); err != nil || closed != "C1/4" {
		t.Errorf("closure_posted on %q %v", closed, err)
	}
}
