package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func TestReleaseBuild(t *testing.T) {
	t.Parallel()
	for v, want := range map[string]bool{
		"v0.1.0":                  true,
		"v0.2.0-rc.1":             true,
		"v0.1.0-3-g763c8dd":       false,
		"v0.1.0-3-g763c8dd-dirty": false,
		"v0.1.0-dirty":            false,
		"763c8dd":                 false,
		"dev":                     false,
	} {
		if got := releaseBuild(v); got != want {
			t.Errorf("releaseBuild(%q) = %v", v, got)
		}
	}
}

// storeEnv is a test env with no daemon and a store of one meta row, mark.
func storeEnv(t *testing.T, mark string) *testEnv {
	t.Helper()
	te := newTestEnv(t, &fake.Manager{})
	te.noDaemon(t)
	db, err := sqlite.Open(te.storePath(), nil)
	if err != nil {
		t.Fatal(err)
	}
	sqlitetest.Exec(t, db, `INSERT INTO meta (key, value) VALUES ('mark', ?)`, mark)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return te
}

func setStore(t *testing.T, path string, stmts ...string) {
	t.Helper()
	db, err := sqlite.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range stmts {
		sqlitetest.Exec(t, db, q)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func markOf(t *testing.T, path string) string {
	t.Helper()
	db, err := sqlite.OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var v string
	if err := db.R().QueryRowContext(context.Background(), `SELECT value FROM meta WHERE key = 'mark'`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func (te *testEnv) runCLI(args ...string) int { return Run(context.Background(), te.Env, args) }

func TestStoreMigrateWithNothingPending(t *testing.T) {
	t.Parallel()
	te := storeEnv(t, "a")
	if code := te.runCLI("store", "migrate"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if got := te.out.String(); got != "store: at schema version "+schemaV+"; nothing to migrate\n" {
		t.Fatalf("%q", got)
	}
}

func TestStoreMigrateRefusesANewerStore(t *testing.T) {
	t.Parallel()
	te := storeEnv(t, "a")
	setStore(t, te.storePath(), `UPDATE meta SET value = '5' WHERE key = 'compatible_version'`, `PRAGMA user_version = 5`)
	if code := te.runCLI("store", "migrate", "--dry-run"); code != 1 {
		t.Fatalf("exit %d", code)
	}
	want := "is at schema version 5, newer than this release reads (" + schemaV + "); upgrade " + brand.Name + " with `brew upgrade " + brand.Name + "`, or go back to the store from before that upgrade; `" + restoreCommand + "` restores it"
	if !strings.Contains(te.err.String(), want) {
		t.Fatalf("%s", te.err)
	}
}

var schemaV = fmt.Sprint(sqlite.SchemaVersion)

func TestStoreRestorePutsTheNewestBackupBack(t *testing.T) {
	t.Parallel()
	te := storeEnv(t, "before")
	backup := filepath.Join(te.backupsPath(), "store-v"+schemaV+"-20261020T120000Z.db")
	if err := os.MkdirAll(te.backupsPath(), 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(te.storePath())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backup, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	setStore(t, te.storePath(), `UPDATE meta SET value = 'after' WHERE key = 'mark'`)

	if code := te.runCLI("store", "restore", "--list"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if !strings.HasPrefix(te.out.String(), "store-v"+schemaV+"-20261020T120000Z.db  version "+schemaV+"  ") {
		t.Fatalf("list %q", te.out)
	}
	te.out.Reset()
	if code := te.runCLI("store", "restore", "--yes"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if got := markOf(t, te.storePath()); got != "before" {
		t.Fatalf("restored mark %q", got)
	}
	kept := filepath.Join(te.backupsPath(), "store-v"+schemaV+"-"+testNow.UTC().Format("20060102T150405Z")+".pre-restore.db")
	if got := markOf(t, kept); got != "after" {
		t.Fatalf("kept mark %q", got)
	}
	out := te.out.String()
	for _, want := range []string{"restore " + backup + " (version " + schemaV + ", taken ", "what changed in " + brand.Name + " since then is lost", "kept the replaced store as " + kept, "store: restored at version " + schemaV} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestStoreRestoreAsksFirst(t *testing.T) {
	te := storeEnv(t, "a")
	prev := storeInteractive
	storeInteractive = func(*Env) bool { return false }
	t.Cleanup(func() { storeInteractive = prev })
	if err := os.MkdirAll(te.backupsPath(), 0o700); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(te.storePath())
	_ = os.WriteFile(filepath.Join(te.backupsPath(), "store-v"+schemaV+"-20261020T120000Z.db"), raw, 0o600)
	if code := te.runCLI("store", "restore"); code != 1 || !strings.Contains(te.err.String(), "store restore asks first; pass --yes when standard input is not a terminal") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
}

func TestStoreRestoreWithoutABackup(t *testing.T) {
	t.Parallel()
	te := storeEnv(t, "a")
	if code := te.runCLI("store", "restore", "--yes"); code != 1 || !strings.Contains(te.err.String(), "no backup in "+te.backupsPath()+" that this release reads") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
}

func TestAStoreCommandWaitsForTheStoreLock(t *testing.T) {
	te := storeEnv(t, "before")
	_ = os.MkdirAll(te.backupsPath(), 0o700)
	raw, _ := os.ReadFile(te.storePath())
	_ = os.WriteFile(filepath.Join(te.backupsPath(), "store-v"+schemaV+"-20261020T120000Z.db"), raw, 0o600)
	unlock, err := sqlite.LockStore(te.storePath())
	if err != nil {
		t.Fatal(err)
	}
	prevWait, prevOS := storeLockWait, goos
	storeLockWait, goos = 50*time.Millisecond, "linux"
	t.Cleanup(func() { storeLockWait, goos = prevWait, prevOS })
	if code := te.runCLI("store", "restore", "--yes"); code != 1 || !strings.Contains(te.err.String(), "daemon has the store open; stop the `"+brand.Command+" serve` you started and run this again") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	unlock()
	te.err.Reset()
	if code := te.runCLI("store", "restore", "--yes"); code != 0 {
		t.Fatalf("exit %d after unlock: %s", code, te.err)
	}
}

func TestTheDaemonOpensACurrentStoreAndReportsIt(t *testing.T) {
	t.Parallel()
	te := storeEnv(t, "a")
	db, unlock, wait, err := te.tryStore(context.Background(), t.Logf)
	if err != nil || db == nil {
		t.Fatalf("%v %+v", err, wait)
	}
	defer unlock()
	defer db.Close()
	if te.storeStatus == nil || te.storeStatus.State != api.StoreOK || te.storeStatus.Version != sqlite.SchemaVersion {
		t.Fatalf("%+v", te.storeStatus)
	}
	if _, err := sqlite.LockStore(te.storePath()); err == nil {
		t.Fatal("the daemon does not hold the store's lock")
	}
}

func TestTheDaemonWaitsOnANewerStoreUntilItChanges(t *testing.T) {
	t.Parallel()
	te := storeEnv(t, "a")
	newer := uint32(sqlite.SchemaVersion + 1)
	setStore(t, te.storePath(), fmt.Sprintf(`UPDATE meta SET value = '%d' WHERE key = 'compatible_version'`, newer), fmt.Sprintf(`PRAGMA user_version = %d`, newer))
	db, _, wait, err := te.tryStore(context.Background(), t.Logf)
	if err != nil || db != nil {
		t.Fatalf("%v", err)
	}
	st := wait.status
	if st.State != api.StoreTooNew || st.Version != newer || st.Fix != restoreCommand || !strings.Contains(st.Message, "newer than this release reads ("+schemaV+")") {
		t.Fatalf("%+v", st)
	}
	if wait.ready() {
		t.Fatal("ready before the store changed")
	}
	if unlock, err := sqlite.LockStore(te.storePath()); err != nil {
		t.Fatalf("the waiting daemon holds the store's lock: %v", err)
	} else {
		unlock()
	}
	raw, err := os.ReadFile(te.storePath())
	if err != nil {
		t.Fatal(err)
	}
	raw[60], raw[61], raw[62], raw[63] = 0, 0, 0, 1
	if err := os.WriteFile(te.storePath(), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if !wait.ready() {
		t.Fatal("not ready once the store moved")
	}
}

func TestTheDaemonWaitsWhileAStoreCommandHoldsTheLock(t *testing.T) {
	te := storeEnv(t, "a")
	prev := storeLockWait
	storeLockWait = 50 * time.Millisecond
	t.Cleanup(func() { storeLockWait = prev })
	unlock, err := sqlite.LockStore(te.storePath())
	if err != nil {
		t.Fatal(err)
	}
	_, _, wait, err := te.tryStore(context.Background(), t.Logf)
	if err != nil || wait.status.State != api.StoreBusy || wait.ready() {
		t.Fatalf("%v %+v", err, wait.status)
	}
	unlock()
	if !wait.ready() {
		t.Fatal("not ready once the lock is free")
	}
}

func TestDoctorChecksAStoreThisReleaseCannotOpen(t *testing.T) {
	t.Parallel()
	te := storeEnv(t, "a")
	setStore(t, te.storePath(), `UPDATE meta SET value = '4' WHERE key = 'compatible_version'`, `PRAGMA user_version = 4`)
	got := te.storeChecks()
	if got[0].mark != markMissing || got[0].fix != restoreCommand || !strings.Contains(got[0].info, "schema version 4, newer than this release reads ("+schemaV+")") {
		t.Fatalf("%+v", got[0])
	}
	if got[1] != (check{markInfo, "store backup", "none yet; one is taken before each migration", ""}) {
		t.Fatalf("%+v", got[1])
	}
	err := te.downWithStore(errDaemonDown)
	var d storeDown
	if !errors.Is(err, errDaemonDown) || !errors.As(err, &d) || !strings.Contains(d.Detail(), "fix with: "+restoreCommand) {
		t.Fatalf("%v", err)
	}
}

func TestAFailedStorePointsAtTheCommandThatGetsPastIt(t *testing.T) {
	t.Parallel()
	te := storeEnv(t, "a")
	opens := sqlite.Status{Version: sqlite.SchemaVersion, Compatible: sqlite.SchemaVersion}
	if st := te.storeFailed(te.storePath(), opens, errors.New("the final check failed")).status; st.State != api.StoreFailed || st.Fix != restoreCommand {
		t.Fatalf("a migrated store that failed its check: %+v", st)
	}
	if st := te.storeFailed(te.storePath(), sqlite.Status{}, errors.New("unreadable")).status; st.Fix != restoreCommand || st.Message != "unreadable" {
		t.Fatalf("an unreadable store: %+v", st)
	}
	behind := sqlite.Status{Version: sqlite.SchemaVersion + 1, Compatible: sqlite.SchemaVersion + 1}
	if st := te.storeFailed(te.storePath(), behind, fmt.Errorf("checking: %w", sqlite.ErrDamaged)).status; st.Fix != restoreCommand {
		t.Fatalf("a damaged store: %+v", st)
	}
	if st := te.storeFailed(te.storePath(), behind, errors.New("a step failed")).status; st.Fix != migrateCommand {
		t.Fatalf("a failed step: %+v", st)
	}
}

func TestAnUnknownStoreNamesTheVersionsThisReleaseReads(t *testing.T) {
	t.Parallel()
	st := tooNewStatus("/s.db", sqlite.Status{Version: 0})
	if st.Message != fmt.Sprintf("the store at /s.db is at schema version 0; this release reads versions 1 to %d only", sqlite.SchemaVersion) {
		t.Fatalf("%q", st.Message)
	}
}
