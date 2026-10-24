package gitx

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// realRepo is a repository with two commits on main, a branch "behind" at the
// first one, a branch "wt" checked out in a linked worktree, and origin/main
// as the upstream of main.
func realRepo(t *testing.T) (dir, worktree, first, second string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir, worktree = t.TempDir(), filepath.Join(t.TempDir(), "wt")
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "-b", "main")
	run("commit", "-q", "--allow-empty", "-m", "one")
	first = run("rev-parse", "HEAD")
	run("commit", "-q", "--allow-empty", "-m", "two")
	second = run("rev-parse", "HEAD")
	run("branch", "behind", first)
	run("worktree", "add", "-q", "-b", "wt", worktree, first)
	run("remote", "add", "origin", dir)
	run("update-ref", "refs/remotes/origin/main", second)
	run("branch", "--set-upstream-to=origin/main", "main")
	return dir, worktree, first, second
}

func TestGitAgainstARealRepository(t *testing.T) {
	dir, worktree, first, second := realRepo(t)
	ctx := context.Background()
	g := New()
	const missing = "0123456789abcdef0123456789abcdef01234567"

	if ok, err := g.HasCommit(ctx, dir, second); !ok || err != nil {
		t.Errorf("HasCommit(present) = %v, %v", ok, err)
	}
	if ok, err := g.HasCommit(ctx, dir, missing); ok || err != nil {
		t.Errorf("HasCommit(missing) = %v, %v", ok, err)
	}
	if tip, err := g.LocalBranchTip(ctx, dir, "behind"); tip != first || err != nil {
		t.Errorf("LocalBranchTip(behind) = %q, %v", tip, err)
	}
	if tip, err := g.LocalBranchTip(ctx, dir, "nope"); tip != "" || err != nil {
		t.Errorf("LocalBranchTip(missing) = %q, %v", tip, err)
	}
	if _, err := g.LocalBranchTip(ctx, filepath.Join(dir, "not-a-repo"), "main"); err == nil {
		t.Error("LocalBranchTip outside a repository did not fail")
	}
	for _, c := range []struct {
		a, b string
		want bool
	}{{first, second, true}, {second, first, false}, {missing, second, false}} {
		if ok, err := g.IsAncestor(ctx, dir, c.a, c.b); ok != c.want || err != nil {
			t.Errorf("IsAncestor(%.7s, %.7s) = %v, %v", c.a, c.b, ok, err)
		}
	}
	if path, err := g.CheckedOutAt(ctx, dir, "wt"); err != nil || filepath.Base(path) != "wt" {
		t.Errorf("CheckedOutAt(wt) = %q, %v", path, err)
	}
	if path, err := g.CheckedOutAt(ctx, worktree, "main"); err != nil || path == "" {
		t.Errorf("CheckedOutAt(main) from the worktree = %q, %v", path, err)
	}
	if path, err := g.CheckedOutAt(ctx, dir, "behind"); path != "" || err != nil {
		t.Errorf("CheckedOutAt(behind) = %q, %v", path, err)
	}
	if b, err := g.UpstreamBranch(ctx, dir); b != "main" || err != nil {
		t.Errorf("UpstreamBranch(main) = %q, %v", b, err)
	}
	if b, err := g.UpstreamBranch(ctx, worktree); b != "" || err != nil {
		t.Errorf("UpstreamBranch(no upstream) = %q, %v", b, err)
	}
	if err := g.UpdateRef(ctx, dir, "refs/heads/behind", second, missing); err == nil {
		t.Error("UpdateRef moved a branch that was not at the old tip")
	}
	if err := g.UpdateRef(ctx, dir, "refs/heads/behind", second, first); err != nil {
		t.Errorf("UpdateRef: %v", err)
	}
	if tip, _ := g.LocalBranchTip(ctx, dir, "behind"); tip != second {
		t.Errorf("behind at %q after UpdateRef", tip)
	}
	if DefaultBranch(dir) != "" {
		t.Error("a repository without origin/HEAD has a default branch")
	}
}
