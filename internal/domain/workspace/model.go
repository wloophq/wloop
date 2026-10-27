package workspace

import (
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/domain/turn"
)

type Repo struct {
	ID   string
	Path string
	// Manager is the workspace manager the repository is registered with.
	Manager string
	// RemoteKey identifies the remote as host/owner/name, e.g.
	// github.com/owner/repo. Empty when the repository has no remote.
	RemoteKey string
	// DefaultBranch is the branch origin/HEAD names; empty when unknown.
	DefaultBranch string
}

// IsDefaultBranch counts main and master besides what origin/HEAD names, which
// may be unknown or stale.
func (r Repo) IsDefaultBranch(branch string) bool {
	return branch != "" && (branch == r.DefaultBranch || branch == "main" || branch == "master")
}

// OwnerName splits a github.com remote key. ok is false for other hosts.
func (r Repo) OwnerName() (owner, name string, ok bool) {
	parts := strings.Split(r.RemoteKey, "/")
	if len(parts) != 3 || !strings.EqualFold(parts[0], "github.com") {
		return "", "", false
	}
	return parts[1], parts[2], true
}

// LocalHost is the host id of a workspace on this machine.
const LocalHost = "local"

type Workspace struct {
	// Manager owns the workspace; ID is its id there.
	Manager     string
	ID          string
	RepoID      string
	Path        string
	DisplayName string
	// Branch is the local branch without refs/heads/.
	Branch string
	// PushBranch is the remote branch the workspace pushes to, when known.
	PushBranch string
	Status     string
	HostID     string
	LinkedPR   int
	IsMain     bool
	// Comment is the note on the workspace's card, on a manager with a board.
	Comment string
	// BranchRenamed is set by Create when the manager put the workspace on
	// another branch than the requested head, which is taken.
	BranchRenamed bool
	// ParentDropped is set by Create when the request named a parent the
	// workspace was not recorded under.
	ParentDropped bool
	// Unread is the card's unread mark.
	Unread bool
	// Created is set when the manager made the worktree, rather than found
	// one another tool made: it owns the worktree over a manager that only
	// lists it.
	Created bool
}

// Local tells whether w is on this machine at an absolute path.
func (w Workspace) Local() bool {
	return w.HostID == LocalHost && path.IsAbs(filepath.ToSlash(w.Path))
}

// MainOnDefault reports whether w is the main checkout of r sitting on the
// default branch, which is never a pull request's workspace: a release pull
// request from it would otherwise run its rounds there.
func MainOnDefault(w Workspace, r Repo) bool {
	return w.IsMain && r.IsDefaultBranch(w.Branch)
}

// PRBranch is the branch by which the workspace matches a pull request. A main
// checkout matches only by the branch it has checked out.
func (w Workspace) PRBranch() string {
	if w.IsMain {
		return w.Branch
	}
	return w.HeadBranch()
}

// HeadBranch is the branch name a pull request from this workspace would
// carry on GitHub.
func (w Workspace) HeadBranch() string {
	if w.PushBranch != "" {
		return w.PushBranch
	}
	return w.Branch
}

type Activity struct {
	// Busy is true when an agent of the workspace is working or waiting for
	// input.
	Busy bool
	// Waiting is true when an agent stopped on a permission prompt or a
	// question: typed text would answer it.
	Waiting    bool
	UserActive bool
	States     []turn.AgentState
}

// FindRepos returns the registered checkouts of a remote, preferring the one
// that owns preferRepoID and then the one whose directory is named after the
// repository.
func FindRepos(repos []Repo, remoteKey, preferRepoID string) []Repo {
	var out []Repo
	for _, r := range repos {
		if strings.EqualFold(r.RemoteKey, remoteKey) {
			out = append(out, r)
		}
	}
	name := path.Base(remoteKey)
	rank := func(r Repo) int {
		switch {
		case preferRepoID != "" && r.ID == preferRepoID:
			return 0
		case strings.EqualFold(path.Base(r.Path), name):
			return 1
		}
		return 2
	}
	slices.SortStableFunc(out, func(a, b Repo) int { return rank(a) - rank(b) })
	return out
}
