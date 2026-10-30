package collect

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"

	"github.com/wloophq/wloop/plugins/github/api"
	"github.com/wloophq/wloop/plugins/github/pr"
	"github.com/wloophq/wloop/plugins/github/scope"
)

// local is a workspace the loop acts on, with the repository it belongs to,
// the pull request the manager links it to and the head branch of the pull
// request it would link.
type local struct {
	src.Workspace
	LinkedPR            int
	owner, name, branch string
}

// headBranch is the branch name a pull request from w would carry on
// GitHub.
func headBranch(w src.Workspace) string {
	if w.PushBranch != "" {
		return w.PushBranch
	}
	return w.Branch
}

// defaultBranch counts main and master besides what origin/HEAD names, which
// may be unknown or stale.
func defaultBranch(r src.Repo, branch string) bool {
	return branch != "" && (branch == r.DefaultBranch || branch == "main" || branch == "master")
}

// locals lists the local workspaces of repositories on GitHub. A listing
// that fails, or that a manager could not finish, is a warning: ok is
// false, and nothing is linked or unlinked on it.
func (c *Collector) locals(ctx context.Context, rep *Report) (out []local, ok bool) {
	if c.WS == nil {
		return nil, false
	}
	listing, err := c.WS.Workspaces(ctx, "")
	if err != nil {
		c.warn(rep, "workspaces: %v", err)
		return nil, false
	}
	if listing.Partial {
		c.warn(rep, "workspaces: a workspace manager did not list them all")
		return nil, false
	}
	repoByID := map[string]src.Repo{}
	for _, r := range listing.Repos {
		repoByID[r.ID] = r
	}
	for _, w := range listing.Workspaces {
		repo := repoByID[w.RepoID]
		if !w.Local || (w.IsMain && defaultBranch(repo, w.Branch)) {
			continue
		}
		owner, name, found := strings.Cut(w.Repo, "/")
		if !found {
			continue
		}
		l := local{Workspace: w, owner: owner, name: name}
		if !w.IsMain {
			l.LinkedPR = linkedPR(w, owner, name)
		}
		l.branch = c.branchOf(w)
		out = append(out, l)
	}
	return out, true
}

// linkedPR is the number of the pull request of owner/name the manager
// links w to; 0 for none.
func linkedPR(w src.Workspace, owner, name string) int {
	for _, u := range w.Units {
		if ref, ok := pr.RefOf(u); ok && strings.EqualFold(ref.Owner, owner) && strings.EqualFold(ref.Repo, name) {
			return ref.Number
		}
	}
	return 0
}

// branchOf is the head branch a workspace's pull request has: a review
// workspace's display name after the review prefix, the sweep's rule.
func (c *Collector) branchOf(w src.Workspace) string {
	prefix := c.cfg().ReviewPrefix
	switch {
	case w.IsMain:
		return w.Branch
	case prefix != "" && strings.HasPrefix(w.DisplayName, prefix):
		return strings.TrimPrefix(w.DisplayName, prefix)
	}
	return headBranch(w)
}

// reviewWorkspace says w is a review workspace: not the main checkout, and
// named with the review prefix.
func (c *Collector) reviewWorkspace(w local) bool {
	prefix := c.cfg().ReviewPrefix
	return !w.IsMain && prefix != "" && strings.HasPrefix(w.DisplayName, prefix)
}

type branchKey struct{ owner, repo, branch string }

// links holds the stored pull requests the workspaces can link to, read once
// for all the workspaces of a run.
type links struct {
	byRef    map[api.Ref]pr.PR
	byBranch map[branchKey][]pr.PR // open pull requests, highest number first
}

func (c *Collector) buildLinks(ctx context.Context, list []local) (*links, error) {
	open, err := c.Store.ListPRs(ctx, true)
	if err != nil {
		return nil, err
	}
	l := &links{byRef: make(map[api.Ref]pr.PR, len(open)), byBranch: map[branchKey][]pr.PR{}}
	for _, p := range open {
		l.byRef[p.Ref] = p
		k := branchKey{p.Ref.Owner, p.Ref.Repo, p.HeadBranch}
		l.byBranch[k] = append(l.byBranch[k], p)
	}
	for _, ps := range l.byBranch {
		slices.SortFunc(ps, func(a, b pr.PR) int { return b.Ref.Number - a.Ref.Number })
	}
	var missing []api.Ref
	for _, w := range list {
		if w.LinkedPR == 0 {
			continue
		}
		if ref := (api.Ref{Owner: w.owner, Repo: w.name, Number: w.LinkedPR}); !slices.Contains(missing, ref) {
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
func (l *links) of(w local) (pr.PR, bool) {
	if w.LinkedPR != 0 {
		p, ok := l.byRef[api.Ref{Owner: w.owner, Repo: w.name, Number: w.LinkedPR}]
		return p, ok
	}
	for _, p := range l.byBranch[branchKey{w.owner, w.name, w.branch}] {
		if !w.IsMain || !p.IsFork {
			return p, true
		}
	}
	return pr.PR{}, false
}

// workspaceReads lists the pull requests the workspaces link that the
// inventory lacks; the stored ones are watched. A pull request GitHub did not
// return is left out until the next slow sweep.
func (c *Collector) workspaceReads(list []local, l *links) []api.Ref {
	var read []api.Ref
	queued := map[api.Ref]bool{}
	for _, w := range list {
		ref := api.Ref{Owner: w.owner, Repo: w.name, Number: w.LinkedPR}
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
func (c *Collector) noteUnreadable(refs []api.Ref) {
	if len(refs) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.missing == nil {
		c.missing = map[api.Ref]bool{}
	}
	for _, ref := range refs {
		c.missing[ref] = true
	}
}

func (c *Collector) unreadable(ref api.Ref) bool {
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
	linked := map[api.Ref]pr.Workspace{}
	before := map[api.Ref]pr.Workspace{}
	for _, w := range list {
		p, found := l.of(w)
		if !found || (w.IsMain && linked[p.Ref].ID != "") {
			continue
		}
		before[p.Ref] = p.Workspace
		linked[p.Ref] = pr.Workspace{
			Manager: w.Manager, ID: w.ID, DisplayName: w.DisplayName, Status: w.Status,
			IsReview:    c.reviewWorkspace(w),
			AgentStates: w.AgentStates,
			Comment:     pr.OneLine(w.Comment, 1000),
			IsMain:      w.IsMain,
		}
	}
	needsMe := c.cfg().NeedsMe
	for ref, ws := range linked {
		old := before[ref]
		if ws.Status == old.Status || (!slices.Contains(needsMe, ws.Status) && !slices.Contains(needsMe, old.Status)) {
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
	var qs []api.BranchQuery
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
		if q := (api.BranchQuery{Owner: w.owner, Repo: w.name, Branch: w.branch}); !slices.Contains(qs, q) {
			qs = append(qs, q)
		}
	}
	if len(qs) == 0 {
		return false, nil
	}
	matches, err := c.GH.FindByBranch(ctx, qs)
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, api.ErrBudget) {
			return false, err
		}
		rep.Failed++
		c.warn(rep, "workspace branches: %v", err)
		return false, nil
	}
	c.noteAsked(asks)
	var refs []api.Ref
	for _, q := range qs {
		if n, _ := api.PickByBranch(matches[q]); n != 0 {
			refs = append(refs, api.Ref{Owner: q.Owner, Repo: q.Repo, Number: n})
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
	var refs []api.Ref
	for _, w := range list {
		p, found := l.of(w)
		if !found || p.State != api.StateOpen || c.reviewWorkspace(w) ||
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
	refs = slices.DeleteFunc(refs, func(r api.Ref) bool {
		d, ok := details[r]
		return !ok || d.ThreadsTotal == 0 || d.ThreadsRead
	})
	return c.fetch(ctx, refs, resolved, now, rep)
}

// recordColumn records status unless it is already the newest status event,
// as when the sweep moved the card itself.
func (c *Collector) recordColumn(ctx context.Context, ref api.Ref, status string) error {
	last, ok, err := c.Store.LastEvent(ctx, ref, pr.KindStatus)
	if err != nil || (ok && last.Data == status) {
		return err
	}
	return c.Store.RecordEvents(ctx, []pr.Event{{PR: ref, Kind: pr.KindStatus, At: c.Now(), Data: status}})
}
