package fsx

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSameFileFollowsLinksAndNeedsBothFiles(t *testing.T) {
	dir := t.TempDir()
	a, b, link := filepath.Join(dir, "a"), filepath.Join(dir, "b"), filepath.Join(dir, "link")
	for _, f := range []string{a, b} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(a, link); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		x, y string
		want bool
	}{
		{a, a, true},
		{a, link, true},
		{a, b, false},
		{a, filepath.Join(dir, "gone"), false},
	} {
		if got := SameFile(c.x, c.y); got != c.want {
			t.Errorf("SameFile(%s, %s) = %v, want %v", c.x, c.y, got, c.want)
		}
	}
}
