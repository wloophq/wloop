// Package git reads local checkouts with the git binary, as the planner
// asks.
package git

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

// Exec runs git on the PATH.
type Exec struct{}

func (Exec) run(ctx context.Context, dir string, args ...string) ([]byte, int, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return out, exit.ExitCode(), err
	}
	return out, 0, err
}

// Upstream is the remote branch the checkout at dir pushes to; "" for none.
func (g Exec) Upstream(ctx context.Context, dir string) (string, error) {
	out, code, err := g.run(ctx, dir, "rev-parse", "--symbolic-full-name", "@{upstream}")
	if code == 128 {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	parts := strings.SplitN(strings.TrimSpace(string(out)), "/", 4)
	if len(parts) < 4 || parts[1] != "remotes" {
		return "", nil
	}
	return parts[3], nil
}

// LocalBranchTip is the commit branch points at in dir; "" when there is
// no such branch.
func (g Exec) LocalBranchTip(ctx context.Context, dir, branch string) (string, error) {
	out, code, err := g.run(ctx, dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch+"^{commit}")
	if code == 1 {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// CheckedOutAt is the worktree that has branch checked out; "" for none.
func (g Exec) CheckedOutAt(ctx context.Context, dir, branch string) (string, error) {
	out, _, err := g.run(ctx, dir, "worktree", "list", "--porcelain")
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

// HasCommit says whether the object database of dir holds the commit.
func (g Exec) HasCommit(ctx context.Context, dir, oid string) (bool, error) {
	_, code, err := g.run(ctx, dir, "rev-parse", "--verify", "--quiet", oid+"^{commit}")
	if code == 1 {
		return false, nil
	}
	return err == nil, err
}

// IsAncestor says whether ancestor is reachable from commit; an unknown
// object is not an ancestor.
func (g Exec) IsAncestor(ctx context.Context, dir, ancestor, commit string) (bool, error) {
	_, code, err := g.run(ctx, dir, "merge-base", "--is-ancestor", ancestor, commit)
	switch {
	case err == nil:
		return true, nil
	case code == 1, code == 128:
		return false, nil
	}
	return false, err
}
