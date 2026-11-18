package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
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
	writeSized(t, filepath.Join(state, "dossiers", "github", "acme", "api%231#", "notes.md"), 100)

	var out bytes.Buffer
	writeDoctorPlain(&out, []checkSection{{"Disk", e.diskChecks(ctx, Defaults())}})
	golden(t, "doctor_disk.golden", strings.ReplaceAll(out.String(), state, "<state>"))
}

func TestDoctorDiskSaysNothingOfDossiersNeverWritten(t *testing.T) {
	t.Parallel()
	e := &Env{StateDir: filepath.Join(t.TempDir(), "state"), Home: t.TempDir(), Now: time.Now}
	for _, c := range e.diskChecks(context.Background(), Defaults()) {
		if strings.Contains(c.name+c.info, "dossier") {
			t.Errorf("check %+v", c)
		}
	}
}
