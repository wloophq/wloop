package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunTightensStateDir(t *testing.T) {
	t.Parallel()
	d := testDeps(t)
	older := map[string]os.FileMode{
		"store.db-wal": 0o644,
		"app.err.log":  0o644,
		"snapshots":    0o755 | os.ModeDir,
	}
	for name, mode := range older {
		p := filepath.Join(d.StateDir, name)
		var err error
		if mode.IsDir() {
			err = os.Mkdir(p, mode.Perm())
		} else {
			err = os.WriteFile(p, []byte("x"), mode.Perm())
		}
		if err == nil {
			err = os.Chmod(p, mode.Perm())
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(d.StateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stop := runServer(t, New(d))
	waitFor(t, func() bool { _, err := os.Stat(d.InfoPath); return err == nil })
	if err := stop(); err != nil {
		t.Fatalf("run: %v", err)
	}
	fi, err := os.Stat(d.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o700 {
		t.Errorf("state directory %v", got)
	}
	for name, mode := range older {
		fi, err := os.Stat(filepath.Join(d.StateDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != mode.Perm() {
			t.Errorf("%s changed to %v", name, fi.Mode().Perm())
		}
	}
}
