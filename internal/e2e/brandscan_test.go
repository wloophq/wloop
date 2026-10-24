package e2e

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/brand/brandtest"
	"github.com/wloophq/wloop/internal/domain/turn"
)

const scanBinaryEnv = "LOOP_BRAND_SCAN_BIN"

func TestNothingWrittenNamesTheDefaultBrand(t *testing.T) {
	bin := os.Getenv(scanBinaryEnv)
	if bin == "" {
		t.Skip(scanBinaryEnv + " names a binary built with the test brand; make scan-brand sets it")
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	tools := filepath.Join(root, "tools")
	for _, d := range []string{filepath.Join(home, ".claude"), tools} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	argv := filepath.Join(root, "claude-argv.txt")
	if err := os.WriteFile(filepath.Join(tools, "claude"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" >> "+argv+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{"HOME=" + home, "XDG_STATE_HOME=" + filepath.Join(home, "state"), "XDG_CONFIG_HOME=" + filepath.Join(home, "config"), "PATH=" + tools + ":/usr/bin:/bin"}
	var written []string
	for _, args := range [][]string{
		{"help"},
		{"version"},
		{"install", "--home", filepath.Join(home, ".claude")},
		{"config", "get", "agent"},
		{"profile", "where", "review"},
		{"no-such-command"},
	} {
		cmd := exec.Command(bin, args...)
		cmd.Env = env
		cmd.Dir = root
		out, _ := cmd.CombinedOutput()
		written = append(written, "$ "+strings.Join(args, " ")+"\n"+string(out))
	}
	var found []string
	for _, text := range written {
		found = append(found, scanLines("output", text)...)
	}
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && p != filepath.Join(tools, "claude") {
			raw, _ := os.ReadFile(p)
			rel, _ := filepath.Rel(root, p)
			found = append(found, scanLines("path", filepath.ToSlash(rel))...)
			found = append(found, scanLines(filepath.ToSlash(rel), string(raw))...)
		}
		return nil
	})
	found = append(found, scanLines("store-dump", scriptedStore(t, filepath.Join(root, "store.db")))...)
	if raw, err := os.ReadFile(filepath.Join(home, ".claude.json")); err != nil || !strings.Contains(string(raw), "mcpServers") {
		t.Fatalf("install registered no MCP server to scan: %v", err)
	}
	for _, f := range found {
		t.Errorf("the program wrote the default brand: %s", f)
	}
}

func scriptedStore(t *testing.T, path string) string {
	t.Helper()
	db, err := sqlite.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1760000000, 0)
	ses := turn.Session{ID: "term_1", WorkspaceID: "ws-1", Managed: true, StartedAt: at, MCPAt: at}
	if err := sqlite.New(db).SaveSession(context.Background(), ses); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(regexp.MustCompile(`[\x20-\x7e]{4,}`).ReplaceAll(raw, []byte("$0\n")))
}

// scanLines returns the lines of text that spell the default brand.
func scanLines(label, text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if brandtest.Spells(line) {
			out = append(out, label+": "+strings.TrimSpace(line))
		}
	}
	return out
}

func TestScanFlagsTheDefaultBrand(t *testing.T) {
	upper := strings.ToUpper(brandtest.DefaultName)
	mixed := upper[:2] + brandtest.DefaultName[2:]
	for _, c := range []struct{ label, text string }{
		{"output", "run '" + brandtest.DefaultName + " install' to move it"},
		{"path", "state/" + brandtest.DefaultName + "/serve.log"},
		{"claude-argv.txt", "mcp add-json " + brandtest.DefaultName},
		{"output", upper + "_HOME is not set"},
		{"output", "start " + mixed},
	} {
		if got := scanLines(c.label, c.text); len(got) != 1 {
			t.Errorf("%s: %q: flagged %q", c.label, c.text, got)
		}
	}
	if got := scanLines("output", "open the ViewLoop"); len(got) != 0 {
		t.Errorf("flagged %q", got)
	}
}
