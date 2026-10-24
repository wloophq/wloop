package claudedesktop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/workspace"
)

func TestReposReadRemoteAndDefaultBranch(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.m.AddRepo(ctx, f.repo); err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.AddRepo(ctx, f.repo); err != nil {
		t.Fatal(err)
	}
	repos, err := f.m.Repos(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := wsdomain.Repo{ID: f.repo, Path: f.repo, RemoteKey: "github.com/acme/api", DefaultBranch: "develop"}
	if len(repos) != 1 || repos[0] != want {
		t.Fatalf("Repos = %+v, want [%+v]", repos, want)
	}
	if _, err := f.m.AddRepo(ctx, t.TempDir()); err == nil {
		t.Fatal("registered a directory that is no git checkout")
	}
}

func TestCreateListsOnlyWorktreesUnderRoot(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if ws, _, err := f.m.Workspaces(ctx); err != nil || len(ws) != 0 {
		t.Fatalf("before any workspace: %+v, %v", ws, err)
	}
	review := f.worktree(t, "review/feat x")
	pr, _, err := f.m.Create(ctx, workspace.CreateRequest{RepoID: f.repo, Name: "pr-7", HeadBranch: "feat/x", BaseRef: "main"})
	if err != nil {
		t.Fatal(err)
	}
	root := f.m.root()
	if review.Path != filepath.Join(root, "api", "review-feat-x") || review.Branch != "review-feat-x" {
		t.Fatalf("review workspace = %+v", review)
	}
	want := wsdomain.Workspace{ID: filepath.Join(root, "api", "pr-7"), RepoID: f.repo, Path: filepath.Join(root, "api", "pr-7"),
		DisplayName: "pr-7", Branch: "feat/x", HostID: wsdomain.LocalHost}
	if pr != want {
		t.Fatalf("Create = %+v, want %+v", pr, want)
	}
	list, scope, err := f.m.Workspaces(ctx)
	if err != nil || !scope.Complete || len(list) != 2 {
		t.Fatalf("Workspaces = %+v, %+v, %v", list, scope, err)
	}
	if _, _, err := f.m.Create(ctx, workspace.CreateRequest{RepoID: f.repo, Name: "again", HeadBranch: "feat/x"}); err == nil {
		t.Fatal("checked out a branch another worktree holds")
	}
}

func TestCreateChecksOutAnExistingBranch(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.m.git(ctx, f.repo, "branch", "leftover"); err != nil {
		t.Fatal(err)
	}
	ws, _, err := f.m.Create(ctx, workspace.CreateRequest{RepoID: f.repo, Name: "w", HeadBranch: "leftover", BaseRef: "origin/pr/1"})
	if err != nil || ws.Branch != "leftover" {
		t.Fatalf("Create = %+v, %v", ws, err)
	}
}

func TestRemoveAndUnregister(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a := f.worktree(t, "a")
	b := f.worktree(t, "b")
	if err := os.WriteFile(filepath.Join(a.Path, "wip.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.m.Remove(ctx, a.ID, false); err == nil {
		t.Fatal("removed a worktree with untracked work without force")
	}
	if err := f.m.Remove(ctx, a.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := f.m.Unregister(ctx, b.ID); err == nil {
		t.Fatal("unregistered a worktree whose checkout is there")
	}
	if err := os.RemoveAll(b.Path); err != nil {
		t.Fatal(err)
	}
	if err := f.m.Unregister(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	out, err := f.m.git(ctx, f.repo, "worktree", "list", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count("\n"+string(out), "\nworktree "); n != 1 {
		t.Fatalf("git still lists %d worktrees:\n%s", n, out)
	}
	if err := f.m.Remove(ctx, f.repo, true); err == nil {
		t.Fatal("removed a path outside the root")
	}
}

func TestReachableNeedsTheApp(t *testing.T) {
	f := newFixture(t)
	if err := f.m.Reachable(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.exec.ps = helperPS
	if err := f.m.Reachable(context.Background()); !errors.Is(err, workspace.ErrUnreachable) {
		t.Fatalf("Reachable without the app = %v", err)
	}
}

func TestCreateKeepsTheDisplayNameAndRenamesATakenHead(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.m.git(ctx, f.repo, "branch", "main-copy"); err != nil {
		t.Fatal(err)
	}
	review := f.worktree(t, "REVIEW feat/x")
	if review.DisplayName != "REVIEW feat/x" {
		t.Fatalf("display name %q", review.DisplayName)
	}
	list, _, err := f.m.Workspaces(ctx)
	if err != nil || !slicesContainName(list, "REVIEW feat/x") {
		t.Fatalf("listing loses the name: %+v, %v", list, err)
	}
	fork, _, err := f.m.Create(ctx, workspace.CreateRequest{RepoID: f.repo, Name: "REVIEW main-copy", HeadBranch: "main-copy", CrossRepository: true})
	if err != nil || fork.Branch != "main-copy-2" || !fork.BranchRenamed {
		t.Fatalf("a fork head colliding with a local branch: %+v, %v", fork, err)
	}
}

func slicesContainName(ws []wsdomain.Workspace, name string) bool {
	for _, w := range ws {
		if w.DisplayName == name {
			return true
		}
	}
	return false
}
