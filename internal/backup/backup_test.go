package backup

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestFileTakesTheFirstFreeName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	var got []string
	for _, body := range []string{"one", "two", "three"} {
		dest, err := File(path, []byte(body), "20261018-093000")
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, dest)
	}
	want := []string{path + ".loop-20261018-093000.bak", path + ".loop-20261018-093000-2.bak", path + ".loop-20261018-093000-3.bak"}
	for i, dest := range want {
		if got[i] != dest {
			t.Fatalf("backups %q, want %q", got, want)
		}
		raw, err := os.ReadFile(dest)
		if err != nil || string(raw) != []string{"one", "two", "three"}[i] {
			t.Fatalf("%s holds %q: %v", dest, raw, err)
		}
		if fi, _ := os.Stat(dest); fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode %v", dest, fi.Mode())
		}
	}
}

func TestFileLeavesForeignBackups(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "settings.json")
	old := target + ".othertool-20250101-000000.bak"
	if err := os.WriteFile(old, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := range 5 {
		if _, err := File(target, []byte("new"), fmt.Sprintf("2026010%d-000000", i)); err != nil {
			t.Fatal(err)
		}
	}
	if raw, err := os.ReadFile(old); err != nil || string(raw) != "old" {
		t.Fatalf("a foreign backup changed: %q %v", raw, err)
	}
}
