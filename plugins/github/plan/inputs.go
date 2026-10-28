package plan

import (
	"context"
	"slices"
	"strings"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/pr"
	"github.com/wloophq/wloop/plugins/github/store"
)

// Git reads what a round would find of a pull request's branches in a
// local checkout.
type Git interface {
	// Upstream is the remote branch the checkout at dir pushes to; "" for
	// none.
	Upstream(ctx context.Context, dir string) (string, error)
	LocalBranchTip(ctx context.Context, dir, branch string) (string, error)
	// CheckedOutAt is the worktree that has branch checked out; "" for none.
	CheckedOutAt(ctx context.Context, dir, branch string) (string, error)
	HasCommit(ctx context.Context, dir, oid string) (bool, error)
	IsAncestor(ctx context.Context, dir, ancestor, commit string) (bool, error)
}

// Inputs are everything the planner reads at one instant: the plugin's
// stored pull requests, and the host's books, round state, events and
// workspaces.
type Inputs struct {
	Now    time.Time
	Viewer string
	Rows   []pr.PR
	// Details are the stored full reads; one of another version is no read.
	Details map[pr.Ref]store.Detail
	// Adopted are the pull requests taken over into the loop.
	Adopted map[pr.Ref]bool
	Books   map[pr.Ref]src.Book
	Rounds  map[pr.Ref]src.RoundState
	Events  map[pr.Ref][]pr.Event
	Listing src.Listing
	// Unlisted says the workspaces could not be read: no round is due.
	Unlisted bool
	// Git nil reads no checkout.
	Git Git
	Cfg Config
}

// facts are a stored pull request as the rules read it, and when its full
// read was made; zero when it has none.
type facts struct {
	pr.Facts
	ReadAt time.Time
}

// workspace is a listed workspace with its repository and the pull request
// its manager links it to.
type workspace struct {
	src.Workspace
	repo        src.Repo
	owner, name string
	linked      int
}

// branch is the branch by which the workspace matches a pull request: a
// main checkout only by the branch it has checked out.
func (w workspace) branch() string {
	if w.IsMain || w.PushBranch == "" {
		return w.Branch
	}
	return w.PushBranch
}

// mainOnDefault is a main checkout on its repository's default branch.
func (w workspace) mainOnDefault() bool {
	return w.IsMain && w.Branch != "" && (w.Branch == w.repo.DefaultBranch || w.Branch == "main" || w.Branch == "master")
}

func (w workspace) of(ref pr.Ref) bool {
	return w.owner != "" && strings.EqualFold(w.owner, ref.Owner) && strings.EqualFold(w.name, ref.Repo)
}

// workspaces are the listing's workspaces of repositories on GitHub.
func workspaces(l src.Listing) []workspace {
	repos := map[string]src.Repo{}
	for _, r := range l.Repos {
		repos[r.ID] = r
	}
	var out []workspace
	for _, w := range l.Workspaces {
		x := workspace{Workspace: w, repo: repos[w.RepoID]}
		if owner, name, ok := ownerName(x.repo.Remote); ok {
			x.owner, x.name = owner, name
		} else if owner, name, ok := strings.Cut(w.Repo, "/"); ok {
			x.owner, x.name = owner, name
		}
		for _, u := range w.Units {
			if ref, ok := pr.RefOf(u); ok && x.of(ref) {
				x.linked = ref.Number
				break
			}
		}
		out = append(out, x)
	}
	return out
}

// ownerName splits a github.com remote key, github.com/<owner>/<name>.
func ownerName(remote string) (owner, name string, ok bool) {
	parts := strings.Split(remote, "/")
	if len(parts) != 3 || !strings.EqualFold(parts[0], "github.com") {
		return "", "", false
	}
	return parts[1], parts[2], true
}

// repoPath is the local checkout of ref's repository: one named after it
// first.
func repoPath(repos []src.Repo, ref pr.Ref) string {
	var found []src.Repo
	for _, r := range repos {
		if owner, name, ok := ownerName(r.Remote); ok && strings.EqualFold(owner, ref.Owner) && strings.EqualFold(name, ref.Repo) {
			found = append(found, r)
		}
	}
	slices.SortStableFunc(found, func(a, b src.Repo) int {
		rank := func(r src.Repo) int {
			if strings.EqualFold(lastPart(r.Path), ref.Repo) {
				return 0
			}
			return 1
		}
		return rank(a) - rank(b)
	})
	if len(found) == 0 {
		return ""
	}
	return found[0].Path
}

func lastPart(p string) string {
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// ActiveBlock is the round_blocked event that still holds among evs, in
// time order: none has cleared it since, by an unblock, a sent or held
// round, or a review of mine after it.
func ActiveBlock(evs []pr.Event, viewer string) (pr.Event, bool) {
	var b pr.Event
	on := false
	for _, e := range evs {
		switch e.Kind {
		case pr.KindRoundBlocked:
			b, on = e, true
		case pr.KindRoundUnblocked, pr.KindRoundSent, pr.KindRoundHeld:
			on = false
		case pr.KindReview:
			if on && viewer != "" && strings.EqualFold(e.Actor, viewer) && e.At.After(b.At) {
				on = false
			}
		}
	}
	return b, on
}

// StartBlock says ev is the host's start block of a round, which names the
// agent's question or an untrusted folder in its key; any other block is
// the planner's.
func StartBlock(ev pr.Event) bool {
	n := len(strings.Split(ev.Key, "\n"))
	return ev.Kind == pr.KindRoundBlocked && (n == 9 || n == 10)
}
