package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/wloophq/wloop/internal/domain/pr"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/config"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	"github.com/wloophq/wloop/internal/github"
)

// aheadReads are the GitHub reads of one pull request an action makes,
// made before it takes the loop lock so that it asks GitHub nothing under it.
type aheadReads struct {
	at        time.Time
	basics    *pr.PullRequest
	basicsErr error
	// full is the read in full readPR returns, when read ahead.
	full    *pr.PullRequest
	fullErr error
	hasFull bool
	viewer  string
}

type aheadKey struct{}

// ReadAhead reads ref's basics and the viewer and returns ctx carrying them,
// with those read ahead before, for the action that takes the loop lock
// next. With full, an open pull request with a review workspace, the one a
// held round reads in full, is also read in full into the inventory.
func (e *Env) ReadAhead(ctx context.Context, ref github.Ref, full bool) context.Context {
	r := &aheadReads{at: e.Now()}
	r.basics, r.basicsErr = e.GitHub.Basics(ctx, ref)
	if v, err := e.storedViewer(ctx); err == nil && v != "" {
		r.viewer = v
	} else if v, err := e.GitHub.Viewer(ctx); err == nil {
		r.viewer = v
	}
	if full && r.basicsErr == nil && r.basics.State == dgh.StateOpen {
		if cfg, err := e.loadConfig(); err != nil {
			r.full, r.fullErr, r.hasFull = nil, err, true
		} else if e.hasReviewWorkspace(ctx, prConfig(cfg, ref, ""), ref, r.basics) {
			r.full, _, r.fullErr = e.fetchPR(ctx, cfg, ref, r.viewer)
			r.hasFull = true
		}
	}
	set := map[github.Ref]*aheadReads{ref: r}
	if prev, _ := ctx.Value(aheadKey{}).(map[github.Ref]*aheadReads); prev != nil {
		for k, v := range prev {
			if k != ref {
				set[k] = v
			}
		}
	}
	return context.WithValue(ctx, aheadKey{}, set)
}

func (e *Env) hasReviewWorkspace(ctx context.Context, cfg config.Config, ref github.Ref, pr *pr.PullRequest) bool {
	repos, err := e.WS.Repos(ctx)
	if err != nil {
		return false
	}
	all, err := e.WS.List(ctx)
	if err != nil {
		return false
	}
	_, found := findReviewWorkspace(cfg, repos, all, ref.RemoteKey(), ref.Number, pr.HeadRefName)
	return found
}

func aheadOf(ctx context.Context, ref github.Ref) *aheadReads {
	set, _ := ctx.Value(aheadKey{}).(map[github.Ref]*aheadReads)
	return set[ref]
}

func aheadViewer(ctx context.Context) string {
	set, _ := ctx.Value(aheadKey{}).(map[github.Ref]*aheadReads)
	for _, r := range set {
		if r.viewer != "" {
			return r.viewer
		}
	}
	return ""
}

// basics is GitHub's basics of ref, as read ahead when they were, refused
// when the inventory saw ref close or move to another head since.
func (e *Env) basics(ctx context.Context, ref github.Ref) (*pr.PullRequest, error) {
	r := aheadOf(ctx, ref)
	if r == nil {
		return e.GitHub.Basics(ctx, ref)
	}
	if r.basicsErr != nil || e.Store == nil {
		return r.basics, r.basicsErr
	}
	row, ok, err := e.Store.PR(ctx, ref)
	if err != nil {
		return nil, err
	}
	if ok && row.UpdatedAt.After(r.at) && (row.State != r.basics.State || row.HeadOid != r.basics.HeadRefOid) {
		return nil, fmt.Errorf("%s changed on GitHub while this waited for the loop; ask again", ref.URL())
	}
	return r.basics, nil
}

// storedOpenPR is ref as the inventory holds it, refused unless open: what
// a turn the loop launches under its lock reads instead of GitHub. ok is
// false when the inventory has no row of ref.
func (e *Env) storedOpenPR(ctx context.Context, ref github.Ref) (pr *pr.PullRequest, ok bool, err error) {
	if e.Store == nil {
		return nil, false, nil
	}
	row, ok, err := e.Store.PR(ctx, ref)
	if err != nil || !ok {
		return nil, false, err
	}
	if row.State != dgh.StateOpen {
		return nil, true, fmt.Errorf("%s is %s", ref.URL(), row.State)
	}
	details, err := e.Store.Details(ctx, []github.Ref{ref})
	if err != nil {
		return nil, true, err
	}
	viewer, err := e.storedViewer(ctx)
	if err != nil {
		return nil, true, err
	}
	return inventory.GitHubOf(row, details[ref], viewer), true, nil
}

// launchPR is the open pull request a turn the loop launches is about: as
// read ahead, else as stored, else read from GitHub for one the inventory
// never held.
func (e *Env) launchPR(ctx context.Context, ref github.Ref) (*pr.PullRequest, error) {
	if aheadOf(ctx, ref) != nil {
		return e.openPR(ctx, ref)
	}
	pr, ok, err := e.storedOpenPR(ctx, ref)
	if ok || err != nil {
		return pr, err
	}
	return e.openPR(ctx, ref)
}

// readPR reads ref in full from GitHub into the inventory and returns it as
// the sweep reads it, with the viewer; as read ahead when it was.
func (e *Env) readPR(ctx context.Context, cfg config.Config, ref github.Ref) (*pr.PullRequest, string, error) {
	if r := aheadOf(ctx, ref); r != nil && r.hasFull {
		return r.full, r.viewer, r.fullErr
	}
	return e.fetchPR(ctx, cfg, ref, aheadViewer(ctx))
}

// coveredPR is ref in full for the reviews a launched round covers: as read
// ahead, else as stored with its detail. It never asks GitHub: a launch
// holds the loop lock.
func (e *Env) coveredPR(ctx context.Context, ref github.Ref) (*pr.PullRequest, string, error) {
	if r := aheadOf(ctx, ref); r != nil && r.hasFull {
		return r.full, r.viewer, r.fullErr
	}
	if e.Store == nil {
		return nil, "", errNoStore
	}
	row, ok, err := e.Store.PR(ctx, ref)
	if err != nil {
		return nil, "", err
	}
	details, err := e.Store.Details(ctx, []github.Ref{ref})
	if err != nil {
		return nil, "", err
	}
	viewer, err := e.storedViewer(ctx)
	if err != nil {
		return nil, "", err
	}
	d, has := details[ref]
	if !ok || !has || viewer == "" {
		return nil, "", fmt.Errorf("the inventory has not read %s in full yet", ref)
	}
	return inventory.GitHubOf(row, d, viewer), viewer, nil
}
