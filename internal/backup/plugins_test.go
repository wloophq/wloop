package backup

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		return "absent"
	}
	return string(raw)
}

func TestBackupIncludesPluginStores(t *testing.T) {
	t.Parallel()
	state, backups := t.TempDir(), t.TempDir()
	write(t, filepath.Join(state, "plugins", "github", "plugin.db"), "github store")
	write(t, filepath.Join(state, "plugins", "github", "plugin.db-wal"), "github wal")
	write(t, filepath.Join(state, "plugins", "slack", "import.db"), "slack export")
	write(t, filepath.Join(state, "plugins", "slack", "import.db.done"), "imported")
	write(t, filepath.Join(state, "plugins", "slack", "agent", "session"), "agent state")
	write(t, filepath.Join(state, "plugins", "checksums"), "abc github")
	b := filepath.Join(backups, "store-v7-20261029T000000Z.db")
	if err := PluginStores(state, b); err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string]string{
		"github/plugin.db": "github store", "github/plugin.db-wal": "github wal", "slack/import.db": "slack export",
		"slack/import.db.done": "absent", "slack/agent/session": "absent", "checksums": "absent",
	} {
		if got := read(t, filepath.Join(PluginsOf(b), rel)); got != want {
			t.Errorf("the backup's %s is %q, want %q", rel, got, want)
		}
	}
	if fi, err := os.Stat(filepath.Join(PluginsOf(b), "github", "plugin.db")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("the backup's store: %v %v, want 0600", fi.Mode().Perm(), err)
	}
	empty := filepath.Join(backups, "store-v6.db")
	if err := PluginStores(t.TempDir(), empty); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(PluginsOf(empty)); err == nil {
		t.Error("a state with no plugin store left a plugins directory beside the backup")
	}
}

func TestRestoreRestoresPluginStores(t *testing.T) {
	t.Parallel()
	state, backups := t.TempDir(), t.TempDir()
	b := filepath.Join(backups, "store-v7-20261029T000000Z.db")
	write(t, filepath.Join(PluginsOf(b), "github", "plugin.db"), "github then")
	write(t, filepath.Join(state, "plugins", "github", "plugin.db"), "github now")
	write(t, filepath.Join(state, "plugins", "github", "plugin.db-wal"), "github wal now")
	write(t, filepath.Join(state, "plugins", "github", "plugin.db-shm"), "github shm now")
	write(t, filepath.Join(state, "plugins", "slack", "plugin.db"), "slack now")
	write(t, filepath.Join(state, "plugins", "slack", "agent", "session"), "agent state")
	write(t, filepath.Join(state, "plugins", "checksums"), "abc github")
	if err := RestorePluginStores(b, state); err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string]string{
		"github/plugin.db": "github then", "github/plugin.db-wal": "absent", "github/plugin.db-shm": "absent",
		"slack/plugin.db": "absent", "slack/agent/session": "agent state", "checksums": "abc github",
	} {
		if got := read(t, filepath.Join(state, "plugins", rel)); got != want {
			t.Errorf("the state's %s is %q, want %q", rel, got, want)
		}
	}
	old := filepath.Join(backups, "store-v6.db")
	if err := RestorePluginStores(old, state); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(state, "plugins", "github"))
	var left []string
	for _, e := range entries {
		left = append(left, e.Name())
	}
	if len(left) != 0 {
		t.Errorf("a backup taken before any plugin store left %q", left)
	}
}
