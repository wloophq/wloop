package gitcheck_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/gitcheck"
	"github.com/wloophq/wloop/internal/core/workspaces"
	wt "github.com/wloophq/wloop/internal/core/workspaces/workspacestest"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/gitx"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// repo is a new repository at dir whose origin is a new bare repository at
// bare.
func repo(t *testing.T, dir, bare string) string {
	t.Helper()
	git(t, filepath.Dir(dir), "init", "-q", "--bare", bare)
	git(t, filepath.Dir(dir), "init", "-q", dir)
	git(t, dir, "remote", "add", "origin", bare)
	return dir
}

// realWorld realizes c with git: a bare origin, the main checkout and the
// workspace as its linked worktree on wt.Branch, pushed with upstream.
func realWorld(t *testing.T, c wt.Checkout) wt.World {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatal("git is required")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	root := t.TempDir()
	main := repo(t, filepath.Join(root, "main"), filepath.Join(root, "origin.git"))
	write(t, filepath.Join(main, ".gitignore"), strings.Join(c.IgnoreLines(), "\n")+"\n")
	write(t, filepath.Join(main, wt.TrackedFile), "readme\n")
	git(t, main, "add", ".")
	git(t, main, "commit", "-q", "-m", "base")
	git(t, main, "push", "-q", "-u", "origin", "HEAD:main")
	dir := filepath.Join(root, "ws")
	git(t, main, "worktree", "add", "-q", "-b", wt.Branch, dir)
	write(t, filepath.Join(dir, "feat.txt"), "work\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "work")
	git(t, dir, "push", "-q", "-u", "origin", wt.Branch)
	for range c.Unpushed {
		git(t, dir, "commit", "-q", "--allow-empty", "-m", "later")
	}
	if c.PRHoldsHead {
		git(t, dir, "push", "-q", "origin", "HEAD:refs/pull/1/head")
	}
	if c.SimilarBranch {
		git(t, dir, "push", "-q", "origin", "HEAD:refs/heads/someone/"+wt.Branch)
	}
	if c.BranchGone {
		git(t, root, "--git-dir", filepath.Join(root, "origin.git"), "update-ref", "-d", "refs/heads/"+wt.Branch)
	}
	for _, p := range c.Modified {
		write(t, filepath.Join(dir, p), "changed\n")
	}
	for p, content := range c.Untracked {
		write(t, filepath.Join(dir, p), content)
	}
	for p, content := range c.Ignored {
		write(t, filepath.Join(dir, p), content)
	}
	for p, content := range c.Main {
		write(t, filepath.Join(main, p), content)
	}
	for _, l := range c.Links {
		if err := os.Symlink(filepath.Join(main, "elsewhere"), filepath.Join(dir, l)); err != nil {
			t.Fatal(err)
		}
	}
	for name, n := range c.Nested {
		nested := filepath.Join(dir, filepath.FromSlash(c.NestedPath(name)))
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatal(err)
		}
		if n.NoRemote {
			git(t, nested, "init", "-q")
		} else {
			repo(t, nested, filepath.Join(root, name+".git"))
		}
		write(t, filepath.Join(nested, ".gitignore"), strings.Join(n.NestedIgnoreLines(), "\n")+"\n")
		write(t, filepath.Join(nested, "a.txt"), "a\n")
		git(t, nested, "add", ".")
		git(t, nested, "commit", "-q", "-m", "nested")
		if !n.NoRemote {
			git(t, nested, "push", "-q", "-u", "origin", "HEAD:main")
		}
		if n.Modified {
			write(t, filepath.Join(nested, "draft.md"), "draft\n")
		}
		for range n.Unpushed {
			git(t, nested, "commit", "-q", "--allow-empty", "-m", "later")
		}
		if n.OtherBranch {
			git(t, nested, "checkout", "-q", "-b", "wip")
			git(t, nested, "commit", "-q", "--allow-empty", "-m", "wip")
			git(t, nested, "checkout", "-q", "main")
		}
		if n.Stash {
			write(t, filepath.Join(nested, "a.txt"), "changed\n")
			git(t, nested, "stash", "-q")
		}
		if n.Detached {
			git(t, nested, "checkout", "-q", "--detach")
			git(t, nested, "commit", "-q", "--allow-empty", "-m", "detached")
		}
		if n.Tagged {
			git(t, nested, "checkout", "-q", "--detach")
			git(t, nested, "commit", "-q", "--allow-empty", "-m", "tagged")
			git(t, nested, "tag", "keep")
			git(t, nested, "checkout", "-q", "main")
		}
		for p, content := range n.Ignored {
			write(t, filepath.Join(nested, filepath.FromSlash(p)), content)
		}
	}
	g := gitcheck.Git{G: gitx.Git{R: gitx.Exec{}}}
	return wt.World{Remote: g, Tree: g, Files: gitcheck.Files{}, Dir: dir, Main: main, Head: git(t, dir, "rev-parse", "HEAD")}
}

func TestTreeContract(t *testing.T)     { wt.RunTree(t, realWorld) }
func TestRemoteContract(t *testing.T)   { wt.RunRemote(t, realWorld) }
func TestFilesContract(t *testing.T)    { wt.RunFiles(t, realWorld) }
func TestRemovalsContract(t *testing.T) { wt.RunRemovals(t, realWorld) }

// committing makes a commit in its directory right after the first Unpushed
// read, as an agent committing during a removal's checks would.
type committing struct {
	workspaces.Remote
	t     *testing.T
	reads int
}

func (c *committing) Unpushed(ctx context.Context, dir string) (int, error) {
	n, err := c.Remote.Unpushed(ctx, dir)
	if c.reads++; c.reads == 1 {
		git(c.t, dir, "commit", "-q", "--allow-empty", "-m", "racing")
	}
	return n, err
}

func TestACommitBetweenCheckAndRemoveRefusesOnARealCheckout(t *testing.T) {
	w := realWorld(t, wt.Checkout{})
	w.Remote = &committing{Remote: w.Remote, t: t}
	s, m := wt.Service(w)
	ws := workspace.Workspace{ID: "ws-1", RepoID: "r1", Path: w.Dir, Branch: wt.Branch, HostID: workspace.LocalHost}
	err := s.Remove(context.Background(), ws, workspaces.PRInfo{}, false)
	var r workspaces.Refusal
	if !errors.As(err, &r) || !strings.Contains(r.Reason, "changed while it was checked") || len(m.Removed()) != 0 {
		t.Fatalf("Remove = %v, removed %v", err, m.Removed())
	}
}

func TestFetchTrackingKeepsOtherRemoteRefs(t *testing.T) {
	w := realWorld(t, wt.Checkout{BranchGone: true, PRHoldsHead: true})
	if err := w.Remote.FetchTracking(context.Background(), w.Dir, 1); err != nil {
		t.Fatal(err)
	}
	refs := strings.Fields(git(t, w.Dir, "for-each-ref", "--format=%(refname)", "refs/remotes"))
	if !slices.Contains(refs, "refs/remotes/origin/main") || !slices.Contains(refs, gitx.PRRef(1)) || slices.Contains(refs, "refs/remotes/origin/"+wt.Branch) {
		t.Fatalf("remote refs %v", refs)
	}
}

func TestADirectoryInsideAMainCheckoutIsNotACheckout(t *testing.T) {
	world := realWorld(t, wt.Checkout{})
	dir := filepath.Join(world.Main, ".claude", "worktrees", "x")
	write(t, filepath.Join(dir, "notes.md"), "draft\n")
	world.Dir = dir
	s, m := wt.Service(world)
	ws := workspace.Workspace{ID: "ws-1", RepoID: "r1", Path: dir, Branch: wt.Branch, HostID: workspace.LocalHost}
	pr := workspaces.PRInfo{Unit: unit.Unit{Provider: unit.GitHub, Key: "acme/api#1"}, Number: 1}
	if r, _, err := s.RemoveRefusal(context.Background(), ws, pr); err != nil || r != workspaces.NotACheckout {
		t.Errorf("RemoveRefusal = %q, %v; want %q", r, err, workspaces.NotACheckout)
	}
	for _, force := range []bool{false, true} {
		var refused workspaces.Refusal
		if err := s.Remove(context.Background(), ws, pr, force); !errors.As(err, &refused) || refused.Reason != workspaces.NotACheckout {
			t.Errorf("Remove(force=%v) = %v", force, err)
		}
	}
	if len(m.Removed()) != 0 {
		t.Errorf("removed %v", m.Removed())
	}
}
