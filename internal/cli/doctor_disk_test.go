package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/domain/dossier"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func writeSized(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDoctorDiskGolden(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	state := filepath.Join(home, ".local", "state", "loop")
	db, err := sqlitetest.Open(filepath.Join(state, storeFile), nil)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	rows := []struct {
		owner  dossier.Owner
		status dossier.Status
		size   int
	}{
		{dossier.Owner{Unit: unit.Unit{Provider: "github", Key: "acme/api#1"}}, dossier.Active, 2 << 20},
		{dossier.Owner{Unit: unit.Unit{Provider: "slack", Key: "T000/C000/1.2"}}, dossier.Resolved, 3000},
		{dossier.Owner{Unit: unit.Unit{Provider: "github", Key: "acme/api#2"}}, dossier.Archived, 0},
		{dossier.Owner{Unit: unit.Unit{Provider: "github", Key: "acme/api#3"}}, dossier.Purged, 0},
	}
	for _, r := range rows {
		rel, err := dossier.Rel(r.owner)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Dossiers().Put(ctx, dossier.Dossier{Owner: r.owner, RelPath: rel, Status: r.status, LastWriteAt: at}); err != nil {
			t.Fatal(err)
		}
		if r.size > 0 {
			writeSized(t, filepath.Join(state, "dossiers", rel, "sources", "a.bin"), r.size)
			writeSized(t, filepath.Join(state, "dossiers", rel, "notes.md"), 100)
		}
		if r.status == dossier.Archived {
			writeSized(t, dossier.Tarball(state, rel), 4096)
		}
	}
	tool := filepath.Join(home, "orca", "workspaces", "api", "fix")
	for _, a := range []sqlite.AgentSession{{ID: "s1", Kind: "claude", Worktree: tool, FirstSeen: at}, {ID: "s2", Kind: "claude", Worktree: tool, FirstSeen: at}} {
		if err := db.SaveAgentSession(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	e := &Env{StateDir: state, Home: home, Now: func() time.Time { return at }}
	project := e.transcriptDir("", tool)
	writeSized(t, filepath.Join(project, "s1.jsonl"), 5000)
	writeSized(t, filepath.Join(project, "s1", "subagents", "agent-1.jsonl"), 1000)
	writeSized(t, filepath.Join(e.transcriptDir("", filepath.Join(home, "elsewhere")), "x.jsonl"), 7000)

	cfg := Defaults()
	cfg.Dossiers.SoftCapMB = 1
	var out bytes.Buffer
	writeDoctorPlain(&out, []checkSection{{"Disk", e.diskChecks(ctx, cfg)}})
	golden(t, "doctor_disk.golden", out.String())
}

func TestDoctorDiskNamesACheckout(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := filepath.Join(home, "src", "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "linked"), 0o700); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(home, ".local", "state")
	if err := os.MkdirAll(filepath.Dir(linked), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(repo, "linked"), linked); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{filepath.Join(repo, "state"), filepath.Join(linked, "loop")} {
		e := &Env{StateDir: state, Home: home, Now: time.Now}
		got := e.diskChecks(context.Background(), Defaults())
		if len(got) == 0 || got[0].mark != markMissing || !bytes.Contains([]byte(got[0].info), []byte("src")) {
			t.Errorf("state %s: checks %+v, want the checkout named first", state, got)
		}
	}
}
