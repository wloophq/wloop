package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

const worktreesV1 = `CREATE TABLE worktrees (
  path TEXT PRIMARY KEY,
  repo TEXT NOT NULL DEFAULT '', manager TEXT NOT NULL, workspace_id TEXT NOT NULL DEFAULT '',
  created_by TEXT NOT NULL DEFAULT '', at TEXT NOT NULL
) WITHOUT ROWID;`

// worktreesV3 is worktrees rebuilt with a CHECK, the kind of change only the
// 12-step rebuild makes; it drops a parent of worktree_units.
const worktreesV3 = `CREATE TABLE worktrees (
  path TEXT PRIMARY KEY,
  repo TEXT NOT NULL DEFAULT '', manager TEXT NOT NULL CHECK (manager != ''), workspace_id TEXT NOT NULL DEFAULT '',
  created_by TEXT NOT NULL DEFAULT '', at TEXT NOT NULL, note TEXT NOT NULL DEFAULT ''
) WITHOUT ROWID;`

// synthetic is a schema set two versions past this release's: version 2
// adds a column older releases ignore, version 3 rebuilds a parent table.
func synthetic(t *testing.T, version uint32) schemaSet {
	t.Helper()
	if !strings.Contains(schema, worktreesV1) {
		t.Fatal("schema.sql no longer holds worktreesV1; update the synthetic migrations")
	}
	v2 := strings.Replace(worktreesV1, "at TEXT NOT NULL\n)", "at TEXT NOT NULL, note TEXT NOT NULL DEFAULT ''\n)", 1)
	ms := []migration{
		{to: 2, name: "worktree_notes", compatible: 1, up: execAll(`ALTER TABLE worktrees ADD COLUMN note TEXT NOT NULL DEFAULT ''`)},
		{to: 3, name: "worktree_manager_check", foreignKeysOff: true, up: execAll(
			strings.Replace(strings.TrimSuffix(worktreesV3, ";"), "CREATE TABLE worktrees", "CREATE TABLE new_worktrees", 1),
			`INSERT INTO new_worktrees SELECT path, repo, manager, workspace_id, created_by, at, note FROM worktrees`,
			`DROP TABLE worktrees`,
			`ALTER TABLE new_worktrees RENAME TO worktrees`,
		)},
	}
	switch version {
	case 1:
		return schemaSet{1, schema, nil}
	case 2:
		return schemaSet{2, strings.Replace(schema, worktreesV1, v2, 1), ms[:1]}
	case 3:
		return schemaSet{3, strings.Replace(schema, worktreesV1, worktreesV3, 1), ms}
	}
	t.Fatalf("no synthetic version %d", version)
	return schemaSet{}
}

func storeAt(t *testing.T, set schemaSet) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state", "store.db")
	db, err := set.open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func migrateOpts(path string) MigrateOptions {
	return MigrateOptions{Backups: filepath.Join(filepath.Dir(path), "backups"), Now: func() time.Time {
		return time.Date(2026, 11, 2, 21, 30, 4, 0, time.UTC)
	}}
}

func versionOf(t *testing.T, path string) uint32 {
	t.Helper()
	db, err := handle(path+"?_pragma=query_only(1)", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var v uint32
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// rawExec runs statements on the store at path outside the package's
// handles, with foreign key enforcement as the DSN leaves it: off.
func rawExec(t *testing.T, path string, stmts ...string) {
	t.Helper()
	db, err := handle(path+"?_pragma=busy_timeout(5000)", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

var sqlComment = regexp.MustCompile(`--[^\n]*`)

// shape describes the structure of the store at path: every table's
// columns, keys and normalized SQL, every index and trigger, the version
// and the compatible version. Two stores of one shape read alike.
func shape(t *testing.T, path string) string {
	t.Helper()
	db, err := handle(path+"?_pragma=query_only(1)", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var b strings.Builder
	lines := func(q string, args ...any) {
		rows, err := db.Query(q, args...)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		defer rows.Close()
		cols, _ := rows.Columns()
		vals := make([]sql.NullString, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		for rows.Next() {
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			for i, v := range vals {
				fmt.Fprintf(&b, "%s=%s ", cols[i], v.String)
			}
			b.WriteString("\n")
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
	}
	var objects []struct{ kind, name, table, sql string }
	rows, err := db.Query(`SELECT type, name, tbl_name, coalesce(sql, '') FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%' ORDER BY type, name`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var o struct{ kind, name, table, sql string }
		if err := rows.Scan(&o.kind, &o.name, &o.table, &o.sql); err != nil {
			t.Fatal(err)
		}
		objects = append(objects, o)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	for _, o := range objects {
		// ALTER TABLE ... RENAME quotes the new name in the stored SQL.
		norm := strings.ReplaceAll(strings.Join(strings.Fields(sqlComment.ReplaceAllString(o.sql, "")), ""), `"`, "")
		fmt.Fprintf(&b, "%s %s on %s: %s\n", o.kind, o.name, o.table, norm)
		if o.kind != "table" {
			continue
		}
		lines(`SELECT * FROM pragma_table_xinfo(?)`, o.name)
		lines(`SELECT * FROM pragma_foreign_key_list(?)`, o.name)
		lines(`SELECT name, "unique", origin, partial FROM pragma_index_list(?) ORDER BY name`, o.name)
	}
	lines(`PRAGMA user_version`)
	lines(`PRAGMA auto_vacuum`)
	lines(`SELECT value FROM meta WHERE key = ?`, compatibleKey)
	return b.String()
}

func TestMigrateTakesEveryPastVersionToTheShapeOfANewStore(t *testing.T) {
	ctx := context.Background()
	v3 := synthetic(t, 3)
	want := shape(t, storeAt(t, v3))
	for _, from := range []uint32{1, 2} {
		path := storeAt(t, synthetic(t, from))
		p, err := v3.migrate(ctx, path, migrateOpts(path))
		if err != nil {
			t.Fatalf("from %d: %v", from, err)
		}
		if p.From != from || p.To != 3 || len(p.Steps) != int(3-from) {
			t.Fatalf("from %d: plan %+v", from, p)
		}
		if got := shape(t, path); got != want {
			t.Fatalf("from %d, the migrated store:\n%s\na new store:\n%s", from, got, want)
		}
		if err := v3.refuse(path, mustInspect(t, v3, path)); err != nil {
			t.Fatalf("from %d, the header after migrating: %v", from, err)
		}
	}
}

func mustInspect(t *testing.T, set schemaSet, path string) Status {
	t.Helper()
	s, err := set.inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestEveryReleasedSchemaMigratesToSchemaSQL builds a store from the schema
// of each released version, frozen in testdata, migrates it with this
// release and compares it with a store schema.sql creates.
func TestEveryReleasedSchemaMigratesToSchemaSQL(t *testing.T) {
	ctx := context.Background()
	want := shape(t, storeAt(t, current))
	for v := uint32(1); v <= SchemaVersion; v++ {
		frozen, err := os.ReadFile(filepath.Join("testdata", fmt.Sprintf("schema-v%d.sql", v)))
		if err != nil {
			t.Fatalf("version %d has no frozen schema: %v", v, err)
		}
		old := schemaSet{v, string(frozen), current.migrations[:v-1]}
		path := storeAt(t, old)
		if _, err := current.migrate(ctx, path, migrateOpts(path)); err != nil {
			t.Fatalf("from version %d: %v", v, err)
		}
		if got := shape(t, path); got != want {
			t.Fatalf("a store from version %d, migrated:\n%s\nschema.sql:\n%s", v, got, want)
		}
	}
}

func TestSchemaSQLIsFrozenAtSchemaVersion(t *testing.T) {
	frozen, err := os.ReadFile(filepath.Join("testdata", fmt.Sprintf("schema-v%d.sql", SchemaVersion)))
	if err != nil {
		t.Fatalf("copy schema.sql to testdata/schema-v%d.sql: %v", SchemaVersion, err)
	}
	if string(frozen) != schema {
		t.Fatalf("schema.sql differs from testdata/schema-v%d.sql: a change to the schema is a new version, with a migration", SchemaVersion)
	}
}

func TestMigrationsAreOneVersionEachUpToSchemaVersion(t *testing.T) {
	if got := len(migrations); got != SchemaVersion-1 {
		t.Fatalf("%d migrations for schema version %d", got, SchemaVersion)
	}
	prev := uint32(1)
	for i, m := range migrations {
		if m.to != uint32(i+2) || m.name == "" || m.up == nil {
			t.Fatalf("migration %d is %+v", i, m)
		}
		c := current.compatibleAt(m.to)
		if c > m.to || c < prev {
			t.Fatalf("%s declares compatible %d after %d", m.label(), c, prev)
		}
		prev = c
	}
}

func TestMigrateKeepsRowsAndForeignKeysThroughARebuild(t *testing.T) {
	ctx := context.Background()
	path := storeAt(t, synthetic(t, 1))
	rawExec(t, path,
		`INSERT INTO worktrees (path, manager, at) VALUES ('/w/a', 'orca', 't')`,
		`INSERT INTO worktree_units (path, provider, unit_key, at) VALUES ('/w/a', 'github', 'acme/api#7', 't')`,
		// An orphan from before enforcement does not block the rebuild.
		`INSERT INTO worktree_units (path, provider, unit_key, at) VALUES ('/w/gone', 'github', 'acme/api#8', 't')`,
	)
	if _, err := synthetic(t, 3).migrate(ctx, path, migrateOpts(path)); err != nil {
		t.Fatal(err)
	}
	db, err := synthetic(t, 3).open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var units int
	var note string
	if err := db.r.QueryRow(`SELECT count(*) FROM worktree_units`).Scan(&units); err != nil || units != 2 {
		t.Fatalf("worktree_units after the rebuild: %d %v", units, err)
	}
	if err := db.r.QueryRow(`SELECT note FROM worktrees WHERE path = '/w/a'`).Scan(&note); err != nil || note != "" {
		t.Fatalf("note %q %v", note, err)
	}
	if _, err := execW(db, `INSERT INTO worktree_units (path, provider, unit_key, at) VALUES ('/w/none', 'github', 'x', 't')`); err == nil {
		t.Fatal("enforcement is off after the migration")
	}
}

func TestAFailedMigrationLeavesTheStoreAtTheLastVersionItReached(t *testing.T) {
	ctx := context.Background()
	set := synthetic(t, 3)
	set.migrations = append([]migration{}, set.migrations...)
	set.migrations[1].up = execAll(`CREATE TABLE half (x)`, `SELECT no_such_function()`)
	path := storeAt(t, synthetic(t, 1))
	o := migrateOpts(path)
	_, err := set.migrate(ctx, path, o)
	var me *MigrationError
	if !errors.As(err, &me) || me.From != 1 || me.Reached != 2 || me.Step != "0003_worktree_manager_check" {
		t.Fatalf("%#v", err)
	}
	if !strings.Contains(err.Error(), "the store migration from version 1 failed at 0003_worktree_manager_check: ") ||
		!strings.Contains(err.Error(), "the store is at version 2, and a copy from before the migration is at "+filepath.Join(o.Backups, "store-v1-20261102T213004Z.db")) {
		t.Fatalf("message: %v", err)
	}
	if v := versionOf(t, path); v != 2 {
		t.Fatalf("version %d", v)
	}
	var n int
	db, _ := handle(path+"?_pragma=query_only(1)", 1)
	defer db.Close()
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE name = 'half'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("the failed step's table: %d %v", n, err)
	}
	if b, _ := Backups(o.Backups); len(b) != 1 || b[0].Version != 1 {
		t.Fatalf("backups %+v", b)
	}
}

func TestARebuildThatBreaksAForeignKeyFails(t *testing.T) {
	ctx := context.Background()
	set := synthetic(t, 3)
	set.migrations = append([]migration{}, set.migrations...)
	set.migrations[1].up = execAll(`INSERT INTO worktree_units (path, provider, unit_key, at) VALUES ('/w/none', 'github', 'x', 't')`)
	path := storeAt(t, synthetic(t, 2))
	_, err := set.migrate(ctx, path, migrateOpts(path))
	if err == nil || !strings.Contains(err.Error(), "it leaves 1 rows that break a foreign key") {
		t.Fatalf("%v", err)
	}
	if v := versionOf(t, path); v != 2 {
		t.Fatalf("version %d", v)
	}
}

func TestPlanChangesNothing(t *testing.T) {
	path := storeAt(t, synthetic(t, 1))
	o := migrateOpts(path)
	p, err := synthetic(t, 3).plan(path, o.Backups, o.Now())
	if err != nil {
		t.Fatal(err)
	}
	if p.From != 1 || p.To != 3 || strings.Join(p.Steps, ",") != "0002_worktree_notes,0003_worktree_manager_check" ||
		p.Backup != filepath.Join(o.Backups, "store-v1-20261102T213004Z.db") || p.Size == 0 || p.Free <= 0 || p.Short() {
		t.Fatalf("%+v", p)
	}
	if v := versionOf(t, path); v != 1 {
		t.Fatalf("version %d", v)
	}
	if _, err := os.Stat(o.Backups); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the backups directory: %v", err)
	}
}

func TestAStoreAtTheCurrentVersionIsLeftAlone(t *testing.T) {
	path := storeAt(t, current)
	o := migrateOpts(path)
	p, err := Migrate(context.Background(), path, o)
	if err != nil || p.Pending() || p.From != SchemaVersion {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := os.Stat(o.Backups); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a backup of a current store: %v", err)
	}
}

func TestNewerStores(t *testing.T) {
	v1 := synthetic(t, 1)
	compatible := storeAt(t, synthetic(t, 2))
	s := mustInspect(t, v1, compatible)
	if s.Version != 2 || s.Compatible != 1 || s.stateFor(1) != StateReadable {
		t.Fatalf("a store whose migration declared version 1 compatible: %+v", s)
	}
	db, err := v1.open(compatible, nil)
	if err != nil {
		t.Fatalf("an older release opens a compatible store: %v", err)
	}
	db.Close()

	newer := storeAt(t, synthetic(t, 3))
	s = mustInspect(t, v1, newer)
	if s.Version != 3 || s.Compatible != 3 || s.stateFor(1) != StateNewer {
		t.Fatalf("%+v", s)
	}
	for name, err := range map[string]error{
		"open": func() error { _, err := v1.open(newer, nil); return err }(),
		"plan": func() error { _, err := v1.plan(newer, t.TempDir(), time.Now()); return err }(),
	} {
		want := fmt.Sprintf("the store at %s is at schema version 3, newer than this release reads (1)", newer)
		var se SchemaError
		if !errors.As(err, &se) || se.State() != StateNewer || err.Error() != want || !errors.Is(err, ErrSchema) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestAnOlderStoreIsRefusedUntilMigrated(t *testing.T) {
	path := storeAt(t, synthetic(t, 1))
	_, err := synthetic(t, 2).open(path, nil)
	want := fmt.Sprintf("the store at %s is at schema version 1; this release migrates it to version 2 before opening it", path)
	var se SchemaError
	if !errors.As(err, &se) || se.State() != StateOlder || err.Error() != want {
		t.Fatalf("%v", err)
	}
}

func TestBackupsArePrunedToTheNewest(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	touch := func(name string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for d := 1; d <= 5; d++ {
		touch(fmt.Sprintf("store-v1-202610%02dT120000Z.db", d))
	}
	touch("store-v2-20261001T120000Z.pre-restore.db")
	touch("store-v2-20261030T120000Z.pre-restore.db")
	touch("unrelated.db")
	if err := prune(dir, 3, now, filepath.Join(dir, "store-v1-20261001T120000Z.db")); err != nil {
		t.Fatal(err)
	}
	all, err := Backups(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, b := range all {
		names = append(names, filepath.Base(b.Path))
	}
	want := "store-v2-20261030T120000Z.pre-restore.db,store-v1-20261005T120000Z.db,store-v1-20261004T120000Z.db,store-v1-20261003T120000Z.db,store-v1-20261001T120000Z.db"
	if strings.Join(names, ",") != want {
		t.Fatalf("kept %s", strings.Join(names, ","))
	}
	if _, err := os.Stat(filepath.Join(dir, "unrelated.db")); err != nil {
		t.Fatal("prune removed a file that is not a backup")
	}
}

func TestBackupNamesNeverCollide(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	first, _ := backupName(dir, 1, now, false)
	if err := os.WriteFile(first, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	second, _ := backupName(dir, 1, now, false)
	if filepath.Base(second) != "store-v1-20261102T120000Z-2.db" {
		t.Fatalf("%s", second)
	}
	pre, _ := backupName(dir, 3, now, true)
	if filepath.Base(pre) != "store-v3-20261102T120000Z.pre-restore.db" {
		t.Fatalf("%s", pre)
	}
}

func TestRestorePutsABackupBackAndKeepsTheStoreItReplaced(t *testing.T) {
	ctx := context.Background()
	path := storeAt(t, synthetic(t, 1))
	rawExec(t, path, `INSERT INTO meta (key, value) VALUES ('mark', 'before')`)
	o := migrateOpts(path)
	if _, err := synthetic(t, 3).migrate(ctx, path, o); err != nil {
		t.Fatal(err)
	}
	rawExec(t, path, `UPDATE meta SET value = 'after' WHERE key = 'mark'`)
	all, _ := Backups(o.Backups)
	got, err := synthetic(t, 1).restore(ctx, path, all[0].Path, o.Backups, o.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got.From.Version != 1 || filepath.Base(got.Kept) != "store-v3-20261102T223004Z.pre-restore.db" {
		t.Fatalf("%+v", got)
	}
	if v := versionOf(t, path); v != 1 {
		t.Fatalf("version %d", v)
	}
	mark := func(p string) string {
		db, _ := handle(p+"?_pragma=query_only(1)", 1)
		defer db.Close()
		var v string
		if err := db.QueryRow(`SELECT value FROM meta WHERE key = 'mark'`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	if mark(path) != "before" || mark(got.Kept) != "after" {
		t.Fatalf("restored %q, kept %q", mark(path), mark(got.Kept))
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != storeMode {
		t.Fatalf("restored store mode %v %v", fi.Mode(), err)
	}
}

func TestRestoreRefusesABackupThisReleaseCannotRead(t *testing.T) {
	path := storeAt(t, synthetic(t, 1))
	newer := storeAt(t, synthetic(t, 3))
	_, err := synthetic(t, 1).restore(context.Background(), path, newer, t.TempDir(), time.Now())
	if !errors.Is(err, ErrSchema) {
		t.Fatalf("%v", err)
	}
	if v := versionOf(t, path); v != 1 {
		t.Fatalf("version %d", v)
	}
}

func TestRestorableSkipsNewerAndPreRestoreCopies(t *testing.T) {
	all := []Backup{
		{Path: "a", Version: SchemaVersion + 1},
		{Path: "b", Version: SchemaVersion, PreRestore: true},
		{Path: "c", Version: SchemaVersion},
	}
	if b, ok := Restorable(all); !ok || b.Path != "c" {
		t.Fatalf("%+v %v", b, ok)
	}
}

func TestLockStoreIsHeldOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	unlock, err := LockStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LockStore(path); !errors.Is(err, ErrStoreBusy) {
		t.Fatalf("a second lock: %v", err)
	}
	unlock()
	again, err := LockStore(path)
	if err != nil {
		t.Fatalf("after unlock: %v", err)
	}
	again()
}

func TestMigrateRefusesWithoutRoomForTheBackup(t *testing.T) {
	prev := freeSpace
	freeSpace = func(string) int64 { return 1000 }
	t.Cleanup(func() { freeSpace = prev })
	path := storeAt(t, synthetic(t, 1))
	o := migrateOpts(path)
	_, err := synthetic(t, 2).migrate(context.Background(), path, o)
	if !errors.Is(err, ErrNoSpace) || !strings.Contains(err.Error(), "and 1 MB is free") {
		t.Fatalf("%v", err)
	}
	if v := versionOf(t, path); v != 1 {
		t.Fatalf("version %d", v)
	}
	if _, err := os.Stat(o.Backups); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a backup without room: %v", err)
	}
}

func TestAnOldBackupThatStaysIsAWarningNotAFailure(t *testing.T) {
	path := storeAt(t, synthetic(t, 1))
	o := migrateOpts(path)
	if err := os.MkdirAll(o.Backups, 0o700); err != nil {
		t.Fatal(err)
	}
	for d := 1; d <= 3; d++ {
		if err := os.WriteFile(filepath.Join(o.Backups, fmt.Sprintf("store-v1-202610%02dT120000Z.db", d)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	prev := removeFile
	removeFile = func(string) error { return os.ErrPermission }
	t.Cleanup(func() { removeFile = prev })
	var warned []error
	o.Warn = func(err error) { warned = append(warned, err) }
	if _, err := synthetic(t, 2).migrate(context.Background(), path, o); err != nil {
		t.Fatal(err)
	}
	if v := versionOf(t, path); v != 2 || len(warned) != 1 || !errors.Is(warned[0], os.ErrPermission) {
		t.Fatalf("version %d, warnings %v", v, warned)
	}
}

func TestFailedMigrationsDoNotPileUpBackups(t *testing.T) {
	set := synthetic(t, 2)
	set.migrations = []migration{{to: 2, name: "broken", up: execAll(`SELECT no_such_function()`)}}
	path := storeAt(t, synthetic(t, 1))
	for i := range 3 {
		o := migrateOpts(path)
		o.Keep = 1
		o.Now = func() time.Time { return time.Date(2026, 11, 2, 21, 30, i, 0, time.UTC) }
		if _, err := set.migrate(context.Background(), path, o); err == nil {
			t.Fatal("the broken migration passed")
		}
	}
	if b, _ := Backups(migrateOpts(path).Backups); len(b) != 1 || filepath.Base(b[0].Path) != "store-v1-20261102T213002Z.db" {
		t.Fatalf("backups %+v", b)
	}
}

func TestRestoreCarriesWhatTheBackupHoldsInItsWAL(t *testing.T) {
	path := storeAt(t, synthetic(t, 1))
	src := storeAt(t, synthetic(t, 1))
	db, err := synthetic(t, 1).open(src, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := execW(db, `INSERT INTO meta (key, value) VALUES ('mark', 'in the wal')`); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(src + "-wal"); err != nil || fi.Size() == 0 {
		t.Fatalf("the source has no WAL to carry: %v", err)
	}
	if _, err := synthetic(t, 1).restore(context.Background(), path, src, t.TempDir(), time.Now()); err != nil {
		t.Fatal(err)
	}
	r, _ := handle(path+"?_pragma=query_only(1)", 1)
	defer r.Close()
	var v string
	if err := r.QueryRow(`SELECT value FROM meta WHERE key = 'mark'`).Scan(&v); err != nil || v != "in the wal" {
		t.Fatalf("%q %v", v, err)
	}
}

func TestRestoreOverAnUnreadableStoreKeepsItsBytes(t *testing.T) {
	path := storeAt(t, synthetic(t, 1))
	backup := storeAt(t, synthetic(t, 1))
	garbage := []byte("not a database, and long enough to have a header of sorts: " + strings.Repeat("x", 100))
	if err := os.WriteFile(path, garbage, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := synthetic(t, 1).restore(context.Background(), path, backup, filepath.Join(t.TempDir(), "backups"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if kept, err := os.ReadFile(got.Kept); err != nil || string(kept) != string(garbage) {
		t.Fatalf("kept %q %v", kept, err)
	}
	if v := versionOf(t, path); v != 1 {
		t.Fatalf("version %d", v)
	}
}

func TestBackupsOfTheSameSecondListNewestFirst(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"store-v1-20261102T120000Z.db", "store-v1-20261102T120000Z-2.db", "store-v1-20261102T120000Z-10.db"} {
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	all, _ := Backups(dir)
	var names []string
	for _, b := range all {
		names = append(names, filepath.Base(b.Path))
	}
	if strings.Join(names, ",") != "store-v1-20261102T120000Z-10.db,store-v1-20261102T120000Z-2.db,store-v1-20261102T120000Z.db" {
		t.Fatalf("%s", strings.Join(names, ","))
	}
}

func TestRefuseNamesTheVersionsThisReleaseReads(t *testing.T) {
	err := Refuse("/s.db", Status{Version: 0})
	if err == nil || err.Error() != fmt.Sprintf("the store at /s.db is at schema version 0; this release reads versions 1 to %d only", SchemaVersion) {
		t.Fatalf("%v", err)
	}
	if Refuse("/s.db", Status{Version: SchemaVersion, Compatible: SchemaVersion}) != nil {
		t.Fatal("a current store refused")
	}
}
