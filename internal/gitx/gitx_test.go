package gitx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultBranchReadsOriginHEAD(t *testing.T) {
	plain := t.TempDir()
	writeFile(t, filepath.Join(plain, ".git", "refs", "remotes", "origin", "HEAD"), "ref: refs/remotes/origin/master\n")

	gitfile, common := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(gitfile, ".git"), "gitdir: "+common+"\n")
	writeFile(t, filepath.Join(common, "refs", "remotes", "origin", "HEAD"), "ref: refs/remotes/origin/develop\n")

	linked := t.TempDir()
	writeFile(t, filepath.Join(plain, ".git", "worktrees", "wt", "commondir"), "../..\n")
	writeFile(t, filepath.Join(linked, ".git"), "gitdir: "+filepath.Join(plain, ".git", "worktrees", "wt")+"\n")

	for dir, want := range map[string]string{plain: "master", gitfile: "develop", linked: "master", t.TempDir(): "", "": ""} {
		if got := DefaultBranch(dir); got != want {
			t.Errorf("DefaultBranch(%s) = %q, want %q", dir, got, want)
		}
	}
}

type cannedGit map[string]struct {
	out string
	err error
}

func (c cannedGit) Run(_ context.Context, _ string, _ []string, args ...string) ([]byte, error) {
	r := c[args[0]]
	return []byte(r.out), r.err
}

func TestLocalBranchTip(t *testing.T) {
	ctx := context.Background()
	for name, c := range map[string]struct {
		run     cannedGit
		want    string
		wantErr bool
	}{
		"present": {run: cannedGit{"rev-parse": {out: "abc\n"}}, want: "abc"},
		"absent":  {run: cannedGit{"rev-parse": {err: &Error{Code: 1}}}},
		"broken":  {run: cannedGit{"rev-parse": {err: &Error{Code: 128, Msg: "not a git repository"}}}, wantErr: true},
		"no git":  {run: cannedGit{"rev-parse": {err: errors.New("exec: git not found")}}, wantErr: true},
	} {
		got, err := Git{R: c.run}.LocalBranchTip(ctx, "/r", "feat/x")
		if got != c.want || (err != nil) != c.wantErr {
			t.Errorf("%s: %q %v", name, got, err)
		}
	}
}

func TestHasCommitAndCheckedOutAt(t *testing.T) {
	ctx := context.Background()
	g := Git{R: cannedGit{
		"rev-parse": {err: &Error{Code: 1}},
		"worktree":  {out: "worktree /src/api\nHEAD 1\nbranch refs/heads/main\n\nworktree /wt/x\nHEAD 2\nbranch refs/heads/feat/x\n\nworktree /wt/d\nHEAD 3\ndetached\n\n"},
	}}
	if ok, err := g.HasCommit(ctx, "/r", "c2"); ok || err != nil {
		t.Fatalf("missing commit: %v %v", ok, err)
	}
	if path, err := g.CheckedOutAt(ctx, "/r", "feat/x"); path != "/wt/x" || err != nil {
		t.Fatalf("checked out at %q %v", path, err)
	}
	if path, err := g.CheckedOutAt(ctx, "/r", "feat/y"); path != "" || err != nil {
		t.Fatalf("not checked out: %q %v", path, err)
	}
}

// fetchRunner fails a fetch with more than one refspec and records the rest.
type fetchRunner struct{ fetches [][]string }

func (f *fetchRunner) Run(_ context.Context, _ string, _ []string, args ...string) ([]byte, error) {
	f.fetches = append(f.fetches, args)
	if len(args) > 4 {
		return nil, &Error{Args: args, Code: 128, Msg: "fatal: couldn't find remote ref refs/heads/gone"}
	}
	return nil, nil
}

func TestFetchPRFetchesThePullRequestAloneWhenAHeadIsGone(t *testing.T) {
	r := &fetchRunner{}
	if err := (Git{R: r}).FetchPR(context.Background(), "/wt", 7, "gone"); err != nil {
		t.Fatal(err)
	}
	if len(r.fetches) != 2 || len(r.fetches[1]) != 4 || r.fetches[1][3] != "+refs/pull/7/head:"+PRRef(7) {
		t.Fatalf("fetches %q", r.fetches)
	}
}
