package claudedesktop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
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
		DisplayName: "pr-7", Branch: "feat/x", HostID: wsdomain.LocalHost, Created: true}
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

func TestListsTheWorktreesTheAppCreated(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	made := filepath.Join(f.repo, ".claude", "worktrees", "brave-lovelace-1a2b3c")
	if _, err := f.m.git(ctx, f.repo, "worktree", "add", "-q", "-b", "claude/brave-lovelace-1a2b3c", made); err != nil {
		t.Fatal(err)
	}
	found := filepath.Join(filepath.Dir(f.repo), "elsewhere")
	if _, err := f.m.git(ctx, f.repo, "worktree", "add", "-q", "-b", "elsewhere", found); err != nil {
		t.Fatal(err)
	}
	archived := filepath.Join(f.repo, ".claude", "worktrees", "archived")
	if _, err := f.m.git(ctx, f.repo, "worktree", "add", "-q", "-b", "archived", archived); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "api-link")
	if err := os.Symlink(f.repo, link); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(f.repo, ".claude", "worktrees", "gone")
	f.store(t, "acct-1", map[string]any{"sessionId": HandleOf(testUUID), "worktreePath": filepath.Join(link, ".claude", "worktrees", "brave-lovelace-1a2b3c")})
	f.store(t, "acct-1", map[string]any{"sessionId": "local_c1b2c3d4-0000-4000-8000-000000000003", "cwd": found})
	f.store(t, "acct-1", map[string]any{"sessionId": "local_c1b2c3d4-0000-4000-8000-000000000004", "worktreePath": gone})
	f.store(t, "acct-1", map[string]any{"sessionId": "local_c1b2c3d4-0000-4000-8000-000000000005", "worktreePath": archived, "isArchived": true})
	own := f.worktree(t, "pr-7")

	list, err := f.m.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []wsdomain.Workspace{
		{ID: own.Path, RepoID: f.repo, Path: own.Path, DisplayName: "pr-7", Branch: "pr-7", HostID: wsdomain.LocalHost, Created: true},
		{ID: made, RepoID: f.repo, Path: made, DisplayName: "brave-lovelace-1a2b3c", Branch: "claude/brave-lovelace-1a2b3c", HostID: wsdomain.LocalHost, Created: true},
	}
	if !slices.Equal(list, want) {
		t.Fatalf("List = %+v, want %+v", list, want)
	}

	if err := os.Remove(f.m.reposPath()); err != nil {
		t.Fatal(err)
	}
	repos, err := f.m.Repos(ctx)
	if err != nil || len(repos) != 1 || repos[0].Path != f.repo || repos[0].RemoteKey != "github.com/acme/api" {
		t.Fatalf("Repos = %+v, %v; want the repository of the app's worktree", repos, err)
	}
}

func TestRemovesAWorktreeTheAppCreated(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	made := filepath.Join(f.repo, ".claude", "worktrees", "brave-lovelace-1a2b3c")
	if _, err := f.m.git(ctx, f.repo, "worktree", "add", "-q", "-b", "claude/brave-lovelace-1a2b3c", made); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(f.repo, ".claude", "worktrees", "not-the-apps")
	if _, err := f.m.git(ctx, f.repo, "worktree", "add", "-q", "-b", "not-the-apps", other); err != nil {
		t.Fatal(err)
	}
	f.store(t, "acct-1", map[string]any{"sessionId": HandleOf(testUUID), "cwd": made, "worktreePath": made})
	if err := f.m.Remove(ctx, other, true); err == nil {
		t.Fatal("removed a worktree the app did not create")
	}
	if err := f.m.Remove(ctx, made, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(made); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the worktree is still there: %v", err)
	}
}

func TestStartsASessionInAWorktreeTheAppCreated(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	made := filepath.Join(f.repo, ".claude", "worktrees", "brave-lovelace-1a2b3c")
	if _, err := f.m.git(ctx, f.repo, "worktree", "add", "-q", "-b", "claude/brave-lovelace-1a2b3c", made); err != nil {
		t.Fatal(err)
	}
	f.store(t, "acct-1", map[string]any{"sessionId": "local_c1b2c3d4-0000-4000-8000-000000000003", "worktreePath": made})
	mint, err := os.ReadFile(filepath.Join("testdata", "mint_output.json"))
	if err != nil {
		t.Fatal(err)
	}
	f.exec.claude = func(Cmd) ([]byte, error) { return mint, nil }
	f.exec.open = func(Cmd) error {
		f.register(t, "registry_desktop.json", map[string]any{"cwd": made})
		return nil
	}
	if _, err := f.m.StartAgentSession(ctx, workspace.StartRequest{WorkspaceID: made, Session: testUUID, Agent: "claude"}); err != nil {
		t.Fatal(err)
	}
	if c := f.exec.ran("claude"); len(c) != 1 || c[0].Dir != made {
		t.Fatalf("mint = %+v", c)
	}
}

func TestCreateFromTheDefaultBaseFetchesItFirst(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.m.git(ctx, f.repo, "commit", "-q", "--allow-empty", "-m", "upstream"); err != nil {
		t.Fatal(err)
	}
	upstream, err := f.m.git(ctx, f.repo, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.git(ctx, f.repo, "reset", "-q", "--hard", "HEAD~1"); err != nil {
		t.Fatal(err)
	}
	f.exec.fetch = func(c Cmd) error {
		_, err := f.m.git(ctx, f.repo, "update-ref", "refs/remotes/origin/develop", strings.TrimSpace(string(upstream)))
		return err
	}
	ws, _, err := f.m.Create(ctx, workspace.CreateRequest{RepoID: f.repo, Name: "fix-login", DefaultBase: true, Parent: "/wt/lead"})
	if err != nil {
		t.Fatal(err)
	}
	fetches := slices.DeleteFunc(f.exec.ran("git"), func(c Cmd) bool { return !slices.Contains(c.Args, "fetch") })
	if len(fetches) != 1 || !slices.Contains(fetches[0].Args, "+refs/heads/develop:refs/remotes/origin/develop") {
		t.Fatalf("fetches = %v", fetches)
	}
	head, err := f.m.git(ctx, ws.Path, "rev-parse", "HEAD")
	if err != nil || string(head) != string(upstream) {
		t.Fatalf("worktree at %s, want origin/develop %s (%v)", head, upstream, err)
	}
	if !ws.ParentDropped {
		t.Fatalf("a parent Claude Desktop cannot keep is not reported: %+v", ws)
	}
}

func TestCreateFromTheDefaultBaseFallsBackToHEADWithoutOriginHEAD(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.m.git(ctx, f.repo, "symbolic-ref", "--delete", "refs/remotes/origin/HEAD"); err != nil {
		t.Fatal(err)
	}
	ws, _, err := f.m.Create(ctx, workspace.CreateRequest{RepoID: f.repo, Name: "fix-login", DefaultBase: true})
	if err != nil {
		t.Fatal(err)
	}
	head, _ := f.m.git(ctx, ws.Path, "rev-parse", "HEAD")
	main, _ := f.m.git(ctx, f.repo, "rev-parse", "HEAD")
	if string(head) != string(main) || ws.ParentDropped {
		t.Fatalf("worktree %+v at %s, want HEAD %s", ws, head, main)
	}
}

func TestCreateFromTheDefaultBaseFailsWhenTheFetchFails(t *testing.T) {
	f := newFixture(t)
	f.exec.fetch = func(Cmd) error { return errors.New("could not read from remote repository") }
	if _, _, err := f.m.Create(context.Background(), workspace.CreateRequest{RepoID: f.repo, Name: "fix-login", DefaultBase: true}); err == nil {
		t.Fatal("created a worktree from a default base it could not fetch")
	}
}
