package workspaces

import (
	"context"
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workspace"
)

// The kinds of the events a removal and an unregistration write.
const (
	KindRemoved      = "workspace_removed"      // Key = workspace id, Data = "branch <b> head <sha>"
	KindUnregistered = "workspace_unregistered" // Key = workspace id, Data = "branch <b> head unknown"
)

// Manager is the workspace manager as removal sees it.
type Manager interface {
	List(ctx context.Context) ([]workspace.Workspace, error)
	Repos(ctx context.Context) ([]workspace.Repo, error)
	Remove(ctx context.Context, id string, force bool) error
	Unregister(ctx context.Context, id string) error
}

// Remote is what the remotes hold of a checkout. Only FetchTracking reaches
// the network; the rest read what the last fetch left.
type Remote interface {
	// Tracking is the remote and branch the checkout's branch tracks: origin
	// and "" when it tracks none.
	Tracking(ctx context.Context, dir string) (remote, branch string, err error)
	// FetchTracking fetches the tracked remote, the pull request's head when
	// number is set, and forgets the tracked branch when the remote no
	// longer has it.
	FetchTracking(ctx context.Context, dir string, number int) error
	RemoteHasBranch(ctx context.Context, dir, remote, branch string) (bool, error)
	// Unpushed counts the commits of HEAD that no remote-tracking ref holds.
	Unpushed(ctx context.Context, dir string) (int, error)
	// PRHolds tells whether prHead contains HEAD; an unknown prHead holds
	// nothing.
	PRHolds(ctx context.Context, dir, prHead string) (bool, error)
}

// Tree is the working tree of a checkout.
type Tree interface {
	// Changed counts modified and untracked files.
	Changed(ctx context.Context, dir string) (int, error)
	// DirtyTracked counts modified tracked files.
	DirtyTracked(ctx context.Context, dir string) (int, error)
	// Ignored lists the untracked files and the ignored paths; an ignored
	// directory, or an untracked repository, is one entry ending in "/",
	// what it holds unlisted.
	Ignored(ctx context.Context, dir string) (untracked, ignored []string, err error)
	Head(ctx context.Context, dir string) (string, error)
	// Kept is what the repository at dir holds that no remote has: commits
	// any local ref or HEAD reaches, a stash, or no remote at all; "" when
	// nothing.
	Kept(ctx context.Context, dir string) (string, error)
}

// SessionGuard is what the agent sessions of a workspace allow.
type SessionGuard interface {
	// Refusal is why a live session of the workspace may not be closed, ""
	// when every one may.
	Refusal(ctx context.Context, workspaceID string) (string, error)
	// CloseAll closes the workspace's live sessions, each recording its
	// resume line.
	CloseAll(ctx context.Context, workspaceID, reason string) error
}

// Files compares and inspects paths on disk.
type Files interface {
	// Same tells whether a and b hold the same bytes; directories compare
	// entry by entry. A missing side is not the same.
	Same(a, b string) (bool, error)
	Exists(path string) bool
	// Link tells whether path is a symbolic link.
	Link(path string) bool
	// Repos lists the git repositories at or below dir by slash path
	// relative to it, "." for dir itself, looking into none of them.
	Repos(dir string) ([]string, error)
}

// Rounds opts a pull request out of its automatic first round.
type Rounds interface {
	SkipFirstRound(ctx context.Context, u unit.Unit) error
}

// FetchLog records when each workspace was last fetched.
type FetchLog interface {
	Last(ctx context.Context, id string) (time.Time, error)
	Mark(ctx context.Context, id string, at time.Time) error
}
