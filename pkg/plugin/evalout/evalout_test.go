package evalout

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOutsideRepositoryRefusesAPathInAGitCheckout(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "worktree.git"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := OutsideRepository("out", filepath.Join(repo, "a", "b")); err == nil || !strings.Contains(err.Error(), "inside the git checkout") {
		t.Fatalf("a path under a checkout that does not exist yet: %v", err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	if err := OutsideRepository("out", filepath.Join(link, "x")); err == nil {
		t.Fatal("a path through a symlink into a checkout")
	}
	if err := OutsideRepository("out", filepath.Join(root, "results")); err != nil {
		t.Fatalf("a path outside any checkout: %v", err)
	}
	if err := OutsideRepository("out", ""); err == nil || err.Error() != "--out is required" {
		t.Fatalf("an empty path: %v", err)
	}
}
