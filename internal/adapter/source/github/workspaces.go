package ghsource

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/config"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/scope"
	"github.com/wloophq/wloop/internal/termfmt"
)

// local is a workspace the loop acts on, with the repository it belongs to
// and the head branch of the pull request it would link.
type local struct {
	wsdomain.Workspace
	owner, name, branch string
}

// locals lists the local workspaces of repositories with a GitHub remote.
// Workspace manager errors are warnings: ok is false.
func (c *Collector) locals(ctx context.Context, rep *Report) (out []local, ok bool) {
	if c.WS == nil {
		return nil, false
	}
	repos, err := c.WS.Repos(ctx)
	if err != nil {
		c.warn(rep, "workspaces: %v", err)
		return nil, false
	}
	list, err := c.WS.List(ctx)
	if err != nil {
		c.warn(rep, "workspaces: %v", err)
		return nil, false
	}
	type repoKey struct{ manager, id string }
	repoByID := map[repoKey]wsdomain.Repo{}
	for _, r := range repos {
		repoByID[repoKey{r.Manager, r.ID}] = r
	}
	for _, w := range list {
		repo := repoByID[repoKey{w.Manager, w.RepoID}]
		if w.HostID != wsdomain.LocalHost || wsdomain.MainOnDefault(w, repo) {
			continue
		}
		if w.IsMain {
			w.LinkedPR = 0
		}
		if owner, name, found := repo.OwnerName(); found {
			out = append(out, local{w, owner, name, c.branchOf(w)})
		}
	}
	return out, true
}

// branchOf is the head branch a workspace's pull request has: a review
// workspace's display name after the review prefix, the sweep's rule.
func (c *Collector) branchOf(w wsdomain.Workspace) string {
	switch {
	case w.IsMain:
		return w.PRBranch()
	case strings.HasPrefix(w.DisplayName, c.Cfg.ReviewPrefix):
		return strings.TrimPrefix(w.DisplayName, c.Cfg.ReviewPrefix)
	}
	return w.HeadBranch()
}

type branchKey struct{ owner, repo, branch string }

// links holds the stored pull requests the workspaces can link to, read once
// for all the workspaces of a run.
type links struct {
	byRef    map[github.Ref]dinv.PR
	byBranch map[branchKey][]dinv.PR // open pull requests, highest number first
}

func (c *Collector) buildLinks(ctx context.Context, list []local) (*links, error) {
	open, err := c.Store.ListPRs(ctx, true)
	if err != nil {
		return nil, err
	}
	l := &links{byRef: make(map[github.Ref]dinv.PR, len(open)), byBranch: map[branchKey][]dinv.PR{}}
	for _, p := range open {
		l.byRef[p.Ref] = p
		k := branchKey{p.Ref.Owner, p.Ref.Repo, p.HeadBranch}
		l.byBranch[k] = append(l.byBranch[k], p)
	}
	for _, ps := range l.byBranch {
		slices.SortFunc(ps, func(a, b dinv.PR) int { return b.Ref.Number - a.Ref.Number })
	}
	var missing []github.Ref
	for _, w := range list {
		if w.LinkedPR == 0 {
			continue
		}
		if ref := (github.Ref{Owner: w.owner, Repo: w.name, Number: w.LinkedPR}); !slices.Contains(missing, ref) {
			if _, ok := l.byRef[ref]; !ok {
				missing = append(missing, ref)
			}
		}
	}
	if len(missing) > 0 {
		stored, err := c.Store.PRs(ctx, missing)
		if err != nil {
			return nil, err
		}
		maps.Copy(l.byRef, stored)
	}
	return l, nil
}

// of takes a main checkout only as the workspace of a pull request from the
// same repository on its branch.
func (l *links) of(w local) (dinv.PR, bool) {
	if w.LinkedPR != 0 {
		p, ok := l.byRef[github.Ref{Owner: w.owner, Repo: w.name, Number: w.LinkedPR}]
		return p, ok
	}
	for _, p := range l.byBranch[branchKey{w.owner, w.name, w.branch}] {
		if !w.IsMain || !p.IsFork {
			return p, true
		}
	}
	return dinv.PR{}, false
}

// workspaceReads lists the pull requests the workspaces link that the
// inventory lacks; the stored ones are watched. A pull request GitHub did not
// return is left out until the next slow sweep.
func (c *Collector) workspaceReads(list []local, l *links) []github.Ref {
	var read []github.Ref
	queued := map[github.Ref]bool{}
	for _, w := range list {
		ref := github.Ref{Owner: w.owner, Repo: w.name, Number: w.LinkedPR}
		if _, found := l.of(w); found || w.LinkedPR == 0 || c.unreadable(ref) || queued[ref] {
			continue
		}
		queued[ref] = true
		read = append(read, ref)
	}
	return read
}

// noteUnreadable remembers refs GitHub did not return, until the next slow
// sweep.
func (c *Collector) noteUnreadable(refs []github.Ref) {
	if len(refs) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.missing == nil {
		c.missing = map[github.Ref]bool{}
	}
	for _, ref := range refs {
		c.missing[ref] = true
	}
}

func (c *Collector) unreadable(ref github.Ref) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.missing[ref]
}

func (c *Collector) forgetUnreadable() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.missing = nil
}

// linkWorkspaces links each workspace to its pull request and records a card
// entering or leaving the needs-me column as a status event, so a card the
// agent parked there has a since.
func (c *Collector) linkWorkspaces(ctx context.Context, list []local, l *links, rep *Report) error {
	activity, err := c.WS.AgentActivity(ctx)
	if err != nil {
		c.warn(rep, "agent activity: %v", err)
	}
	linked := map[github.Ref]dinv.Workspace{}
	before := map[github.Ref]dinv.Workspace{}
	for _, w := range list {
		pr, found := l.of(w)
		if !found || (w.IsMain && linked[pr.Ref].ID != "") {
			continue
		}
		before[pr.Ref] = pr.Workspace
		linked[pr.Ref] = dinv.Workspace{
			Manager: w.Manager, ID: w.ID, DisplayName: w.DisplayName, Status: w.Status,
			IsReview:    !w.IsMain && strings.HasPrefix(w.DisplayName, c.Cfg.ReviewPrefix),
			AgentStates: activity[w.ID].States,
			Comment:     termfmt.OneLine(w.Comment, 1000),
			IsMain:      w.IsMain,
		}
	}
	needsMe := c.Cfg.Column(c.Sink, config.ColumnNeedsMe)
	for ref, ws := range linked {
		old := before[ref]
		if needsMe == "" || ws.Status == old.Status || (ws.Status != needsMe && old.Status != needsMe) {
			continue
		}
		if err := c.recordColumn(ctx, ref, ws.Status); err != nil {
			return err
		}
	}
	return c.Store.ReplaceWorkspaces(ctx, linked)
}

// discoverWorkspaceBranches reads the pull request of each workspace that
// links none and whose branch matches nothing stored, when exactly one open
// pull request has that branch, so the next fast sweep links it.
func (c *Collector) discoverWorkspaceBranches(ctx context.Context, resolved scope.Resolved, now time.Time, rep *Report) error {
	list, ok := c.locals(ctx, rep)
	if !ok {
		return nil
	}
	l, err := c.buildLinks(ctx, slices.DeleteFunc(slices.Clone(list), func(w local) bool { return w.LinkedPR != 0 }))
	if err != nil {
		return err
	}
	_, err = c.findBranches(ctx, list, l, false, resolved, now, rep)
	return err
}

// branchAsk is a workspace whose branch a fast run searched once.
type branchAsk struct{ id, branch string }

// findBranches searches the branch of each workspace of list that links no
// pull request and matches none in l, and reads the pull request a search
// picks. With once, a fast run's, it searches each workspace and branch once
// per process. It tells whether it read any.
func (c *Collector) findBranches(ctx context.Context, list []local, l *links, once bool, resolved scope.Resolved, now time.Time, rep *Report) (bool, error) {
	var qs []github.BranchQuery
	var asks []branchAsk
	for _, w := range list {
		if w.LinkedPR != 0 || w.IsMain || w.branch == "" {
			continue
		}
		if _, found := l.of(w); found {
			continue
		}
		ask := branchAsk{w.ID, w.branch}
		if once && c.asked(ask) {
			continue
		}
		asks = append(asks, ask)
		if q := (github.BranchQuery{Owner: w.owner, Repo: w.name, Branch: w.branch}); !slices.Contains(qs, q) {
			qs = append(qs, q)
		}
	}
	if len(qs) == 0 {
		return false, nil
	}
	matches, err := c.GH.FindByBranch(ctx, qs)
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, dgh.ErrBudget) {
			return false, err
		}
		rep.Failed++
		c.warn(rep, "workspace branches: %v", err)
		return false, nil
	}
	c.noteAsked(asks)
	var refs []github.Ref
	for _, q := range qs {
		if n, _ := github.PickByBranch(matches[q]); n != 0 {
			refs = append(refs, github.Ref{Owner: q.Owner, Repo: q.Repo, Number: n})
		}
	}
	return len(refs) > 0, c.fetch(ctx, refs, resolved, now, rep)
}

func (c *Collector) asked(a branchAsk) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.searched[a]
}

func (c *Collector) noteAsked(asks []branchAsk) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.searched == nil {
		c.searched = map[branchAsk]bool{}
	}
	for _, a := range asks {
		c.searched[a] = true
	}
}

// readDevelopedThreads reads again in full the open pull requests someone
// else authored that a workspace other than a review workspace links and
// whose stored detail has threads it did not read: the read before the link
// did not ask for them.
func (c *Collector) readDevelopedThreads(ctx context.Context, list []local, l *links, resolved scope.Resolved, now time.Time, rep *Report) error {
	var refs []github.Ref
	for _, w := range list {
		p, found := l.of(w)
		if !found || p.State != dgh.StateOpen || (!w.IsMain && strings.HasPrefix(w.DisplayName, c.Cfg.ReviewPrefix)) ||
			strings.EqualFold(p.Author, resolved.Viewer) || slices.Contains(refs, p.Ref) {
			continue
		}
		refs = append(refs, p.Ref)
	}
	if len(refs) == 0 {
		return nil
	}
	details, err := c.Store.Details(ctx, refs)
	if err != nil {
		return err
	}
	refs = slices.DeleteFunc(refs, func(r github.Ref) bool {
		d, ok := details[r]
		return !ok || d.ThreadsTotal == 0 || d.ThreadsRead
	})
	return c.fetch(ctx, refs, resolved, now, rep)
}

// recordColumn records status unless it is already the newest status event,
// as when the sweep moved the card itself.
func (c *Collector) recordColumn(ctx context.Context, ref github.Ref, status string) error {
	last, ok, err := c.Store.LastEvent(ctx, ref, dinv.KindStatus)
	if err != nil || (ok && last.Data == status) {
		return err
	}
	return c.Store.RecordEvent(ctx, dinv.Event{PR: ref, Kind: dinv.KindStatus, At: c.Now(), Data: status})
}
