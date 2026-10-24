package gitx

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

func wantSpawns(t *testing.T, sc *meter.Scope, want int64) {
	t.Helper()
	if n := metertest.Count(sc, metertest.Limit{Res: meter.Git, Field: metertest.Spawns}); n != want {
		t.Errorf("%d git spawns, want %d", n, want)
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command(Binary(), append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestReadHeadWorktreeAndPacked(t *testing.T) {
	dir, worktree, first, second := realRepo(t)

	check := func(label, at, wantOid, wantBranch string) {
		t.Helper()
		oid, branch, ok := ReadHead(at)
		if !ok || oid != wantOid || branch != wantBranch {
			t.Errorf("%s: ReadHead = %q, %q, %v; want %q, %q", label, oid, branch, ok, wantOid, wantBranch)
		}
		if want := git(t, at, "rev-parse", "HEAD"); want != oid {
			t.Errorf("%s: git says %q", label, want)
		}
	}
	check("main loose", dir, second, "main")
	check("linked worktree", worktree, first, "wt")

	git(t, dir, "pack-refs", "--all", "--prune")
	check("main packed", dir, second, "main")
	check("linked worktree packed", worktree, first, "wt")

	git(t, worktree, "checkout", "-q", "--detach", second)
	check("detached", worktree, second, "")

	if tip, absent, ok := readBranchTip(dir, "behind"); !ok || absent || tip != first {
		t.Errorf("packed branch tip = %q, %v, %v", tip, absent, ok)
	}
	if _, absent, ok := readBranchTip(dir, "nope"); !ok || !absent {
		t.Errorf("missing branch = absent %v, ok %v", absent, ok)
	}
	if _, _, ok := ReadHead(t.TempDir()); ok {
		t.Error("ReadHead answered for a directory that is not a repository")
	}
}

func TestReadUpstreamMatchesGit(t *testing.T) {
	dir, worktree, _, _ := realRepo(t)
	git(t, worktree, "branch", "--set-upstream-to=origin/main")
	for _, at := range []string{dir, worktree} {
		want, err := New().R.Run(context.Background(), at, nil, "rev-parse", "--symbolic-full-name", "@{upstream}")
		if err != nil {
			t.Fatal(err)
		}
		got, ok := ReadUpstream(at)
		if !ok || "refs/remotes/origin/"+got != strings.TrimSpace(string(want)) {
			t.Errorf("ReadUpstream(%s) = %q, %v; git says %s", at, got, ok, want)
		}
	}
	git(t, worktree, "branch", "--unset-upstream")
	if got, ok := ReadUpstream(worktree); !ok || got != "" {
		t.Errorf("no upstream: %q, %v", got, ok)
	}
}

func TestUpstreamWithoutSpawn(t *testing.T) {
	dir, _, _, _ := realRepo(t)
	var dirs []string
	for range 16 {
		wt := t.TempDir() + "/wt"
		git(t, dir, "worktree", "add", "-q", "-b", fmt.Sprintf("w%d", len(dirs)), wt)
		dirs = append(dirs, wt)
	}
	ctx, sc := metertest.Start(t, context.Background())
	g := New()
	for _, d := range dirs {
		if b, err := g.UpstreamBranch(ctx, d); b != "" || err != nil {
			t.Fatalf("UpstreamBranch(%s) = %q, %v", d, b, err)
		}
		if _, err := g.Head(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	wantSpawns(t, sc, 0)
}

func TestIsAncestorMemo(t *testing.T) {
	dir, _, first, second := realRepo(t)
	ctx, sc := metertest.Start(t, context.Background())
	g := New()
	for range 2 {
		if ok, err := g.IsAncestor(ctx, dir, first, second); !ok || err != nil {
			t.Fatalf("IsAncestor = %v, %v", ok, err)
		}
		if ok, err := g.IsAncestor(ctx, dir, second, first); ok || err != nil {
			t.Fatalf("IsAncestor reversed = %v, %v", ok, err)
		}
	}
	wantSpawns(t, sc, 2)

	ctx, sc = metertest.Start(t, context.Background())
	const missing = "0123456789abcdef0123456789abcdef01234567"
	for range 2 {
		if ok, _ := g.IsAncestor(ctx, dir, missing, second); ok {
			t.Fatal("unknown commit is an ancestor")
		}
	}
	wantSpawns(t, sc, 2)
}

func TestReadUpstreamAgreesWithGit(t *testing.T) {
	std := "\n[remote \"origin\"]\n\turl = /x\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n"
	for name, c := range map[string]struct {
		config    string
		wantAsked bool
	}{
		"standard":           {config: std + "[branch \"main\"]\n\tremote = origin\n\tmerge = refs/heads/main\n"},
		"merge other name":   {config: std + "[branch \"main\"]\n\tremote = origin\n\tmerge = refs/heads/other\n"},
		"dot remote":         {config: std + "[branch \"main\"]\n\tremote = .\n\tmerge = refs/heads/behind\n"},
		"no upstream":        {config: std + "[branch \"other\"]\n\tremote = origin\n\tmerge = refs/heads/main\n"},
		"custom refspec":     {config: "\n[remote \"origin\"]\n\tfetch = +refs/heads/main:refs/remotes/origin/trunk\n[branch \"main\"]\n\tremote = origin\n\tmerge = refs/heads/main\n", wantAsked: true},
		"no remote section":  {config: "[branch \"main\"]\n\tremote = origin\n\tmerge = refs/heads/main\n", wantAsked: true},
		"inline comment":     {config: std + "[branch \"main\"]\n\tremote = origin # c\n\tmerge = refs/heads/main\n", wantAsked: true},
		"upper case header":  {config: std + "[Branch \"main\"]\n\tremote = origin\n\tmerge = refs/heads/main\n", wantAsked: true},
		"legacy header":      {config: std + "[branch.main]\n\tremote = origin\n\tmerge = refs/heads/main\n", wantAsked: true},
		"continuation":       {config: std + "[branch \"main\"]\n\tremote = origin\n\tmerge = refs/heads/\\\nmain\n", wantAsked: true},
		"trailing on header": {config: std + "[branch \"main\"] remote = origin\n\tmerge = refs/heads/main\n", wantAsked: true},
		"upper case key":     {config: std + "[branch \"main\"]\n\tRemote = origin\n\tMerge = refs/heads/main\n"},
		"include":            {config: std + "[include]\n\tpath = /nope\n[branch \"main\"]\n\tremote = origin\n\tmerge = refs/heads/main\n", wantAsked: true},
		"worktree config":    {config: std + "[extensions]\n\tworktreeConfig = true\n[branch \"main\"]\n\tremote = origin\n\tmerge = refs/heads/main\n", wantAsked: true},
		"second fetch":       {config: std + "\tfetch = +refs/pull/*/head:refs/remotes/origin/pr/*\n[branch \"main\"]\n\tremote = origin\n\tmerge = refs/heads/main\n", wantAsked: true},
	} {
		t.Run(name, func(t *testing.T) {
			dir, _, _, _ := realRepo(t)
			cfg := filepath.Join(dir, ".git", "config")
			base, err := os.ReadFile(cfg)
			if err != nil {
				t.Fatal(err)
			}
			head := strings.SplitN(string(base), "[remote", 2)[0]
			head = strings.SplitN(head, "[branch", 2)[0]
			if err := os.WriteFile(cfg, []byte(head+c.config), 0o644); err != nil {
				t.Fatal(err)
			}
			want, err := spawnedUpstream(dir)
			if err != nil {
				t.Fatal(err)
			}
			got, ok := ReadUpstream(dir)
			if ok == c.wantAsked {
				t.Fatalf("ok = %v, want %v", ok, !c.wantAsked)
			}
			if ok && got != want {
				t.Errorf("ReadUpstream = %q, git says %q", got, want)
			}
		})
	}
}

func TestReadHeadFallsBack(t *testing.T) {
	dir, _, _, _ := realRepo(t)
	gd := filepath.Join(dir, ".git")
	set := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(gd, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	set("HEAD", "ref: refs/remotes/origin/main\n")
	if _, _, ok := ReadHead(dir); ok {
		t.Error("a HEAD on a non-branch ref was answered")
	}
	set("HEAD", "ref: refs/heads/unborn\n")
	if _, _, ok := ReadHead(dir); ok {
		t.Error("an unborn branch was answered")
	}
	set("HEAD", "ref: refs/heads/main\n")
	set("refs/heads/main", "ref: refs/heads/behind\n")
	if _, _, ok := ReadHead(dir); ok {
		t.Error("a symbolic loose ref was answered")
	}
	if _, absent, ok := readBranchTip(dir, "main"); ok || absent {
		t.Errorf("a symbolic loose ref: absent %v ok %v", absent, ok)
	}
}

func TestIsAncestorDoesNotRememberRefs(t *testing.T) {
	dir, _, first, second := realRepo(t)
	ctx, sc := metertest.Start(t, context.Background())
	g := New()
	git(t, dir, "update-ref", "refs/remotes/origin/pr/1", first)
	if ok, _ := g.IsAncestor(ctx, dir, second, "refs/remotes/origin/pr/1"); ok {
		t.Fatal("second is not an ancestor of first")
	}
	git(t, dir, "update-ref", "refs/remotes/origin/pr/1", second)
	if ok, _ := g.IsAncestor(ctx, dir, second, "refs/remotes/origin/pr/1"); !ok {
		t.Error("a moved ref kept its old answer")
	}
	wantSpawns(t, sc, 2)
}

func spawnedUpstream(dir string) (string, error) {
	out, err := Exec{}.Run(context.Background(), dir, nil, "rev-parse", "--symbolic-full-name", "@{upstream}")
	if err != nil {
		return "", nil
	}
	parts := strings.SplitN(strings.TrimSpace(string(out)), "/", 4)
	if len(parts) < 4 || parts[1] != "remotes" {
		return "", nil
	}
	return parts[3], nil
}
