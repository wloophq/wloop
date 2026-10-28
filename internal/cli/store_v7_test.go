package cli

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/domain/dossier"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

const dossierHex = "0a1b2c3d4e5f6a7b"

// dossierTree is every file under the dossiers root of stateDir, by its path from there.
func dossierTree(t *testing.T, stateDir string) []string {
	t.Helper()
	root := dossier.Root(stateDir)
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, path)
		out = append(out, filepath.ToSlash(rel))
		return err
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}

func writeTree(t *testing.T, stateDir string, files ...string) {
	t.Helper()
	for _, f := range files {
		path := filepath.Join(dossier.Root(stateDir), filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(f), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRenameDossierDirsMovesEntryToTask(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTree(t, dir,
		"entry/e-"+dossierHex+"#/notes.md",
		"entry/e-"+dossierHex+"#/log/1.md",
		".archive/entry/e-9999888877776666#.tar.gz",
		"slack/T1/C1/1.1#/notes.md",
	)
	if err := renameDossierDirs(dir, "entry", "task"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		".archive/task/t-9999888877776666#.tar.gz",
		"slack/T1/C1/1.1#/notes.md",
		"task/t-" + dossierHex + "#/log/1.md",
		"task/t-" + dossierHex + "#/notes.md",
	}
	if got := dossierTree(t, dir); !slices.Equal(got, want) {
		t.Fatalf("dossiers %q, want %q", got, want)
	}
	if err := renameDossierDirs(dir, "entry", "task"); err != nil {
		t.Fatalf("a second run: %v", err)
	}
	if err := renameDossierDirs(dir, "task", "entry"); err != nil {
		t.Fatal(err)
	}
	back := []string{
		".archive/entry/e-9999888877776666#.tar.gz",
		"entry/e-" + dossierHex + "#/log/1.md",
		"entry/e-" + dossierHex + "#/notes.md",
		"slack/T1/C1/1.1#/notes.md",
	}
	if got := dossierTree(t, dir); !slices.Equal(got, back) {
		t.Fatalf("dossiers back %q, want %q", got, back)
	}
	if err := renameDossierDirs(t.TempDir(), "entry", "task"); err != nil {
		t.Fatalf("a state directory without dossiers: %v", err)
	}
}

// dossierOwners are the owners of the dossiers table of the store at path.
func dossierOwners(t *testing.T, path string) []string {
	t.Helper()
	db, err := sqlite.OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.R().QueryContext(context.Background(), `SELECT owner FROM dossiers ORDER BY owner`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

const v6Dossier = `INSERT INTO dossiers (owner, rel_path, status) VALUES ('entry/e-` + dossierHex + `', 'entry/e-` + dossierHex + `#', 'live')`

func TestMigrateInDaemonRenamesDossierDirs(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.noDaemon(t)
	sqlitetest.At(t, te.storePath(), 6, v6Dossier)
	writeTree(t, te.StateDir, "entry/e-"+dossierHex+"#/notes.md")
	if err := te.migrateInDaemon(context.Background(), te.storePath(), t.Logf); err != nil {
		t.Fatal(err)
	}
	if got := dossierTree(t, te.StateDir); !slices.Equal(got, []string{"task/t-" + dossierHex + "#/notes.md"}) {
		t.Fatalf("dossiers %q", got)
	}
	if got := dossierOwners(t, te.storePath()); !slices.Equal(got, []string{"task/t-" + dossierHex}) {
		t.Fatalf("owners %q", got)
	}
}

func TestStoreMigrateRenamesDossierDirs(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.noDaemon(t)
	sqlitetest.At(t, te.storePath(), 6, v6Dossier)
	writeTree(t, te.StateDir, "entry/e-"+dossierHex+"#/notes.md")
	if code := te.runCLI("store", "migrate", "--yes"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if got := dossierTree(t, te.StateDir); !slices.Equal(got, []string{"task/t-" + dossierHex + "#/notes.md"}) {
		t.Fatalf("dossiers %q", got)
	}
}

func TestRestoringAV6BackupRestoresEntryDossierDirs(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.noDaemon(t)
	backup := filepath.Join(te.backupsPath(), "store-v6-20261020T120000Z.db")
	// The unit marks of task t-<hex> and entry e-<hex> collide once the entry is a task, so the migration
	// after the restore fails and the store stays at version 6.
	sqlitetest.At(t, backup, 6, v6Dossier,
		`INSERT INTO unit_marks (provider, unit_key, mark, value) VALUES ('entry', 'e-`+dossierHex+`', 'm', 'v'), ('task', 't-`+dossierHex+`', 'm', 'v')`)
	setStore(t, te.storePath(), `INSERT INTO dossiers (owner, rel_path, status) VALUES ('task/t-`+dossierHex+`', 'task/t-`+dossierHex+`#', 'live')`)
	writeTree(t, te.StateDir, "task/t-"+dossierHex+"#/notes.md")
	if code := te.runCLI("store", "restore", "--yes", backup); code == 0 {
		t.Fatalf("the migration after the restore passed: %s", te.out)
	}
	if s, err := sqlite.Inspect(te.storePath()); err != nil || s.Version != 6 {
		t.Fatalf("the restored store %+v %v", s, err)
	}
	if got := dossierTree(t, te.StateDir); !slices.Equal(got, []string{"entry/e-" + dossierHex + "#/notes.md"}) {
		t.Fatalf("dossiers %q, want them where the version 6 store names them", got)
	}
}
