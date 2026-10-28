package ghsource

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/config"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workspace"
)

// WorkspaceRows are the stored pull requests, open or not, and the user's
// login on GitHub ("" when not known yet).
type WorkspaceRows interface {
	Rows(ctx context.Context) ([]dinv.PR, error)
	Viewer(ctx context.Context) (string, error)
}

// WorkspaceOverrides are the user's overrides, read for a taken-over pull
// request.
type WorkspaceOverrides interface {
	Overrides(ctx context.Context) (map[unit.Unit]dinv.Override, error)
}

// Holdings are the pull requests the workspaces hold, matched as the sweep
// matches them: by the manager's link, else by the head branch.
type Holdings struct {
	PRs       WorkspaceRows
	Overrides WorkspaceOverrides
}

func (h Holdings) Held(ctx context.Context, snap *workspace.Snapshot, cfg config.Config) (workspace.Held, error) {
	rows, err := h.PRs.Rows(ctx)
	if err != nil {
		return nil, err
	}
	overrides, err := h.Overrides.Overrides(ctx)
	if err != nil {
		return nil, err
	}
	viewer, err := h.PRs.Viewer(ctx)
	if err != nil {
		return nil, err
	}
	return newIndex(snap, rows, overrides, viewer, cfg), nil
}

// Of is the pull request w holds.
func (ix *index) Of(w workspace.Workspace) (workspace.Holding, bool) {
	linked, ok := ix.prOf(w)
	if !ok {
		return workspace.Holding{}, false
	}
	out := workspace.Holding{Unit: linked.Ref.Unit(), Repo: linked.Ref.Owner + "/" + linked.Ref.Repo, Review: ix.reviews(w, linked),
		State: linked.State, Duplicate: ix.duplicate(w, linked.Ref)}
	if linked.State == dgh.StateMerged || linked.State == dgh.StateClosed {
		out.Ended = latest(linked.MergedAt, linked.ClosedAt)
	}
	return out, true
}

func latest(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

type branchKey struct{ owner, repo, branch string }

// index is one snapshot's workspaces matched against the stored pull
// requests.
type index struct {
	snap     *workspace.Snapshot
	cfg      config.Config
	viewer   string
	repos    map[string]workspace.Repo
	byRef    map[pr.Ref]dinv.PR
	adopted  map[unit.Unit]bool
	byBranch map[branchKey][]dinv.PR // open first, then highest number
}

// repoKey is a repository's key in the index: ids are per manager.
func repoKey(manager, id string) string { return manager + "\x00" + id }

func newIndex(snap *workspace.Snapshot, rows []dinv.PR, overrides map[unit.Unit]dinv.Override, viewer string, cfg config.Config) *index {
	ix := &index{snap: snap, cfg: cfg, viewer: viewer, repos: map[string]workspace.Repo{}, byRef: map[pr.Ref]dinv.PR{},
		adopted: map[unit.Unit]bool{}, byBranch: map[branchKey][]dinv.PR{}}
	for u, o := range overrides {
		if o.Adopted {
			ix.adopted[u] = true
		}
	}
	for _, r := range snap.Repos {
		ix.repos[repoKey(r.Manager, r.ID)] = r
	}
	for _, p := range rows {
		ix.byRef[p.Ref] = p
		k := branchKey{strings.ToLower(p.Ref.Owner), strings.ToLower(p.Ref.Repo), p.HeadBranch}
		ix.byBranch[k] = append(ix.byBranch[k], p)
	}
	for _, ps := range ix.byBranch {
		slices.SortFunc(ps, func(a, b dinv.PR) int {
			if ao, bo := a.State == dgh.StateOpen, b.State == dgh.StateOpen; ao != bo {
				if ao {
					return -1
				}
				return 1
			}
			return b.Ref.Number - a.Ref.Number
		})
	}
	return ix
}

func (ix *index) ownerName(w workspace.Workspace) (owner, name string, ok bool) {
	return ix.repos[repoKey(w.Manager, w.RepoID)].OwnerName()
}

// reviewPrefixed tells a review workspace by its name, the sweep's rule.
func (ix *index) reviewPrefixed(w workspace.Workspace) bool {
	if w.IsMain {
		return false
	}
	owner, name, _ := ix.ownerName(w)
	prefix := ix.cfg.For(owner, name).GitHub.ReviewPrefix
	return prefix != "" && strings.HasPrefix(w.DisplayName, prefix)
}

// reviews tells a workspace of someone else's pull request: by the pull
// request's author when it and the viewer are known, else by the review
// prefix of its name. A taken-over pull request and a main checkout are the
// user's.
func (ix *index) reviews(w workspace.Workspace, p dinv.PR) bool {
	if w.IsMain || ix.adopted[p.Ref.Unit()] {
		return false
	}
	if p.Author != "" && ix.viewer != "" {
		return !strings.EqualFold(p.Author, ix.viewer)
	}
	return ix.reviewPrefixed(w)
}

// branchOf is the head branch w's pull request has: a review workspace's
// name after the review prefix, the sweep's rule.
func (ix *index) branchOf(w workspace.Workspace) string {
	if ix.reviewPrefixed(w) {
		owner, name, _ := ix.ownerName(w)
		return strings.TrimPrefix(w.DisplayName, ix.cfg.For(owner, name).GitHub.ReviewPrefix)
	}
	return w.PRBranch()
}

// prOf is w's pull request: Orca's link, else the stored one on its branch,
// open first. The inventory's own link is not read: it outlives the pull
// request it was made for. A main checkout on the default branch
// has none, and a main checkout takes no fork's.
func (ix *index) prOf(w workspace.Workspace) (dinv.PR, bool) {
	repo := ix.repos[repoKey(w.Manager, w.RepoID)]
	if workspace.MainOnDefault(w, repo) {
		return dinv.PR{}, false
	}
	owner, name, ok := repo.OwnerName()
	if ok && w.LinkedPR != 0 && !w.IsMain {
		ref := pr.Ref{Owner: owner, Repo: name, Number: w.LinkedPR}
		if p, found := ix.byRef[ref]; found {
			return p, true
		}
		return dinv.PR{Ref: ref}, true
	}
	if !ok {
		return dinv.PR{}, false
	}
	for _, p := range ix.byBranch[branchKey{strings.ToLower(owner), strings.ToLower(name), ix.branchOf(w)}] {
		if !w.IsMain || !p.IsFork {
			return p, true
		}
	}
	return dinv.PR{}, false
}

// duplicate tells a workspace of ref that is not the one Orca links to it
// while another is.
func (ix *index) duplicate(w workspace.Workspace, ref pr.Ref) bool {
	if w.IsMain || w.LinkedPR == ref.Number {
		return false
	}
	for _, o := range ix.snap.Workspaces {
		if o.Path == w.Path || o.IsMain || o.LinkedPR != ref.Number {
			continue
		}
		if owner, name, ok := ix.ownerName(o); ok && strings.EqualFold(owner, ref.Owner) && strings.EqualFold(name, ref.Repo) {
			return true
		}
	}
	return false
}
