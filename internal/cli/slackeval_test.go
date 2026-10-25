package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/source/slack"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
)

func TestOutputsRefuseARepository(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(repo, "sub", "out")
	outside := t.TempDir()
	link := filepath.Join(outside, "link")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	fixtures, err := filepath.Abs(filepath.Join("..", "adapter", "source", "slack", "eval", "testdata", "fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string][]string{
		"export":            {"slack", "eval", "export", "--since", "2026-10-01", "--until", "2026-10-02", "--out", inside},
		"run":               {"slack", "eval", "run", "--fixtures", fixtures, "--out", inside},
		"score":             {"slack", "eval", "score", "--out", inside, "--label", "x"},
		"optimize":          {"slack", "eval", "optimize", "--out", inside, "--label", "x", "--prompt", "p.md", "--new", filepath.Join(outside, "n.md")},
		"through a symlink": {"slack", "eval", "score", "--out", filepath.Join(link, "sub", "out"), "--label", "x"},
		"optimize new":      {"slack", "eval", "optimize", "--out", outside, "--label", "x", "--prompt", "p.md", "--new", filepath.Join(repo, "n.md")},
	} {
		t.Run(name, func(t *testing.T) {
			te := newSlackTestEnv(t)
			if code := te.run(args...); code == 0 || !strings.Contains(te.err.String(), filepath.Base(repo)) || !strings.Contains(te.err.String(), "git checkout") {
				t.Fatalf("exit %d: %s", code, te.err)
			}
			if _, err := os.Stat(filepath.Join(repo, "sub")); !os.IsNotExist(err) {
				t.Errorf("something was written inside the checkout")
			}
		})
	}
}

func TestSlackEvalUsage(t *testing.T) {
	t.Parallel()
	te := newSlackTestEnv(t)
	if code := te.run("slack", "eval"); code == 0 || !strings.Contains(te.err.String(), "slack eval run") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
}

func TestSlackEvalExportReadsTheStoreWithoutWritingIt(t *testing.T) {
	te := newSlackTestEnv(t)
	path := filepath.Join(te.StateDir, storeFile)
	db, err := sqlitetest.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{slack.CursorViewerTeam: "T1", slack.CursorViewerUser: "U1"} {
		if err := db.Slack().SetCursor(context.Background(), name, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	if code := te.run("slack", "eval", "export", "--since", "2026-10-01", "--until", "2026-10-02", "--out", t.TempDir()); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if !strings.Contains(te.out.String(), "0 fixtures written") {
		t.Fatalf("%s", te.out)
	}
}
