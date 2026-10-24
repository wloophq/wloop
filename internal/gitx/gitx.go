// Package gitx runs the few git commands the loop needs.
package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/meter"
)

type Runner interface {
	Run(ctx context.Context, dir string, env []string, args ...string) ([]byte, error)
}

// Metered records one meter.Git sample, a spawn, per command of Next. A run
// that ends because ctx was canceled is not an error.
type Metered struct{ Next Runner }

func (m Metered) Run(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
	start := time.Now()
	out, err := m.Next.Run(ctx, dir, env, args...)
	x := meter.Sample{Res: meter.Git, Op: subcommand(args), Spawn: true, Err: err != nil && ctx.Err() == nil, Dur: time.Since(start)}
	if meter.Tracing(ctx) {
		x.Detail = strings.Join(args, " ")
	}
	meter.Add(ctx, x)
	return out, err
}

func subcommand(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return "git"
}

type Exec struct{}

func (Exec) Run(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	if len(env) > 0 {
		cmd.Env = append(cmd.Environ(), env...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		code := -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		}
		return stdout.Bytes(), &Error{Args: args, Msg: msg, Code: code}
	}
	return stdout.Bytes(), nil
}

type Error struct {
	Args []string
	Msg  string
	Code int
}

func (e *Error) Error() string { return fmt.Sprintf("git %s: %s", strings.Join(e.Args, " "), e.Msg) }

// Git runs git through R; C, when set, remembers ancestry answers.
type Git struct {
	R Runner
	C *Cache
}

func New() Git { return Git{R: Metered{Next: Exec{}}, C: &Cache{}} }

func PRRef(number int) string { return fmt.Sprintf("refs/remotes/origin/pr/%d", number) }

// DirtyTracked reports modified tracked files; untracked files do not block a
// reset.
func (g Git) DirtyTracked(ctx context.Context, dir string) (bool, error) {
	out, err := g.R.Run(ctx, dir, nil, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) != "", nil
}

// Changed counts modified and untracked files, whatever
// status.showUntrackedFiles says.
func (g Git) Changed(ctx context.Context, dir string) (int, error) {
	out, err := g.R.Run(ctx, dir, nil, "status", "--porcelain", "--untracked-files=all")
	return lineCount(out), err
}

// Ignored lists the paths git ignores in the checkout; a wholly ignored
// directory is one entry ending in "/".
func (g Git) Ignored(ctx context.Context, dir string) ([]string, error) {
	out, err := g.R.Run(ctx, dir, nil, "status", "--porcelain", "-z", "--ignored", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, line := range strings.Split(string(out), "\x00") {
		if p, ok := strings.CutPrefix(line, "!! "); ok {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

// FetchTracking fetches the remote the current branch tracks, origin when it
// tracks none, and returns that remote and the tracked branch, "" for none.
// It does not prune: the pull request refs under refs/remotes are shared by
// every worktree of the repository.
func (g Git) FetchTracking(ctx context.Context, dir string) (remote, branch string, err error) {
	remote = "origin"
	if out, err := g.R.Run(ctx, dir, nil, "rev-parse", "--symbolic-full-name", "@{upstream}"); err == nil {
		if parts := strings.SplitN(strings.TrimSpace(string(out)), "/", 4); len(parts) == 4 && parts[1] == "remotes" {
			remote, branch = parts[2], parts[3]
		}
	}
	_, err = g.R.Run(ctx, dir, nil, "fetch", "-q", remote)
	return remote, branch, err
}

// RemoteHasBranch asks the remote whether branch still exists there.
func (g Git) RemoteHasBranch(ctx context.Context, dir, remote, branch string) (bool, error) {
	out, err := g.R.Run(ctx, dir, nil, "ls-remote", "--heads", remote, branch)
	return strings.TrimSpace(string(out)) != "", err
}

// Unpushed counts the commits of HEAD that no remote branch holds.
func (g Git) Unpushed(ctx context.Context, dir string) (int, error) {
	out, err := g.R.Run(ctx, dir, nil, "rev-list", "HEAD", "--not", "--remotes")
	return lineCount(out), err
}

func lineCount(out []byte) int {
	s := strings.TrimSpace(string(out))
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func (g Git) Head(ctx context.Context, dir string) (string, error) {
	if oid, _, ok := ReadHead(dir); ok {
		return oid, nil
	}
	out, err := g.R.Run(ctx, dir, nil, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// UpstreamBranch returns the remote branch the current branch tracks, or ""
// when it tracks none.
func (g Git) UpstreamBranch(ctx context.Context, dir string) (string, error) {
	if branch, ok := ReadUpstream(dir); ok {
		return branch, nil
	}
	out, err := g.R.Run(ctx, dir, nil, "rev-parse", "--symbolic-full-name", "@{upstream}")
	if err != nil {
		var gitErr *Error
		if errors.As(err, &gitErr) && gitErr.Code == 128 {
			return "", nil
		}
		return "", err
	}
	parts := strings.SplitN(strings.TrimSpace(string(out)), "/", 4)
	if len(parts) < 4 || parts[1] != "remotes" {
		return "", nil
	}
	return parts[3], nil
}

// IsAncestor reports whether ancestor is reachable from commit. An unknown
// object counts as not an ancestor.
func (g Git) IsAncestor(ctx context.Context, dir, ancestor, commit string) (bool, error) {
	common := commonDir(dir)
	key := ancestryKey{common, ancestor, commit}
	memo := isOid(ancestor) && isOid(commit) && !isShallow(common)
	if memo {
		if answer, ok := g.C.ancestor(key); ok {
			return answer, nil
		}
	}
	_, err := g.R.Run(ctx, dir, nil, "merge-base", "--is-ancestor", ancestor, commit)
	if err == nil {
		if memo {
			g.C.remember(key, true)
		}
		return true, nil
	}
	var gitErr *Error
	if errors.As(err, &gitErr) && gitErr.Code == 1 {
		if memo {
			g.C.remember(key, false)
		}
		return false, nil
	}
	if errors.As(err, &gitErr) && gitErr.Code == 128 {
		return false, nil
	}
	return false, err
}

// FetchPR fetches the pull request's head into PRRef and, in the same
// fetch, each of heads, branches of origin, into its remote-tracking ref.
// A head origin lacks fails the whole fetch, so then it fetches the pull
// request alone.
func (g Git) FetchPR(ctx context.Context, dir string, number int, heads ...string) error {
	args := []string{"fetch", "-q", "origin", fmt.Sprintf("+refs/pull/%d/head:%s", number, PRRef(number))}
	refspecs := slices.Clone(args)
	for _, h := range heads {
		refspecs = append(refspecs, headRefspec(h))
	}
	_, err := g.R.Run(ctx, dir, nil, refspecs...)
	if err == nil || len(heads) == 0 || ctx.Err() != nil {
		return err
	}
	_, err = g.R.Run(ctx, dir, nil, args...)
	return err
}

func headRefspec(branch string) string {
	return fmt.Sprintf("+refs/heads/%s:refs/remotes/origin/%s", branch, branch)
}

// TrackBranch renames the checkout's current branch to the PR's head branch and
// tracks it, so tools that find a pull request by branch name find this one.
// It does nothing when another local branch already has that name.
func (g Git) TrackBranch(ctx context.Context, dir, branch string) (renamed bool, err error) {
	return g.track(ctx, dir, branch, true)
}

// TrackFetchedBranch is TrackBranch for a branch FetchPR just fetched as a head.
func (g Git) TrackFetchedBranch(ctx context.Context, dir, branch string) (renamed bool, err error) {
	return g.track(ctx, dir, branch, false)
}

func (g Git) track(ctx context.Context, dir, branch string, fetch bool) (renamed bool, err error) {
	out, err := g.R.Run(ctx, dir, nil, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return false, err
	}
	current := strings.TrimSpace(string(out))
	if current != branch {
		if _, err := g.R.Run(ctx, dir, nil, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
			return false, nil
		}
	}
	if fetch {
		if _, err := g.R.Run(ctx, dir, nil, "fetch", "-q", "origin", headRefspec(branch)); err != nil {
			return false, err
		}
	}
	if current != branch {
		if _, err := g.R.Run(ctx, dir, nil, "branch", "-m", current, branch); err != nil {
			return false, err
		}
		renamed = true
	}
	_, err = g.R.Run(ctx, dir, nil, "branch", "--set-upstream-to=origin/"+branch)
	return renamed, err
}

// LocalBranchTip is the commit of a local branch, empty when there is none.
func (g Git) LocalBranchTip(ctx context.Context, dir, branch string) (string, error) {
	if oid, absent, ok := readBranchTip(dir, branch); ok {
		if absent {
			return "", nil
		}
		return oid, nil
	}
	out, err := g.R.Run(ctx, dir, nil, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch+"^{commit}")
	if err != nil {
		var gitErr *Error
		if errors.As(err, &gitErr) && gitErr.Code == 1 {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// HasCommit reports whether the object database holds the commit.
func (g Git) HasCommit(ctx context.Context, dir, oid string) (bool, error) {
	_, err := g.R.Run(ctx, dir, nil, "rev-parse", "--verify", "--quiet", oid+"^{commit}")
	var gitErr *Error
	if errors.As(err, &gitErr) && gitErr.Code == 1 {
		return false, nil
	}
	return err == nil, err
}

// CheckedOutAt is the path of the worktree that has branch checked out,
// empty when none has.
func (g Git) CheckedOutAt(ctx context.Context, dir, branch string) (string, error) {
	out, err := g.R.Run(ctx, dir, nil, "worktree", "list", "--porcelain")
	if err != nil {
		return "", err
	}
	var path string
	for line := range strings.SplitSeq(string(out), "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			path = p
		} else if line == "branch refs/heads/"+branch {
			return path, nil
		}
	}
	return "", nil
}

// UpdateRef moves ref to newOid only if it still points at oldOid.
func (g Git) UpdateRef(ctx context.Context, dir, ref, newOid, oldOid string) error {
	_, err := g.R.Run(ctx, dir, nil, "update-ref", ref, newOid, oldOid)
	return err
}

func (g Git) ResetHard(ctx context.Context, dir, ref string) error {
	_, err := g.R.Run(ctx, dir, nil, "reset", "--hard", "-q", ref)
	return err
}

// LsRemote checks that the remote is reachable without prompting, the way
// launchd would run it.
func (g Git) LsRemote(ctx context.Context, dir string) error {
	_, err := g.R.Run(ctx, dir, []string{"GIT_SSH_COMMAND=ssh -o BatchMode=yes", "GIT_TERMINAL_PROMPT=0"},
		"ls-remote", "-q", "origin", "HEAD")
	return err
}

// DefaultBranch is the branch origin/HEAD of the checkout at dir names, read
// from the repository files so a sweep spawns no git process per repository.
// It is empty when origin/HEAD is not set and for a reftable repository, which
// keeps no ref files.
func DefaultBranch(dir string) string {
	if dir == "" {
		return ""
	}
	gitDir := commonDir(dir)
	b, err := os.ReadFile(filepath.Join(gitDir, "refs", "remotes", "origin", "HEAD"))
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.TrimSpace(string(b)), "ref: refs/remotes/origin/")
}

// WorktreeAddDetached checks out commit in a new worktree at path, on no
// branch.
func (g Git) WorktreeAddDetached(ctx context.Context, dir, path, commit string) error {
	_, err := g.R.Run(ctx, dir, nil, "worktree", "add", "--detach", path, commit)
	return err
}

func (g Git) WorktreeRemove(ctx context.Context, dir, path string) error {
	_, err := g.R.Run(ctx, dir, nil, "worktree", "remove", "--force", path)
	return err
}

func isShallow(common string) bool {
	_, err := os.Stat(filepath.Join(common, "shallow"))
	return err == nil
}
