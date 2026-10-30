package plan

import (
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/plugins/github/pr"
)

// targets are the pull requests the workspaces stand for: review
// workspaces by link or by branch, and author workspaces by link or among
// my pull requests by branch.
type targets struct {
	review map[pr.Ref]workspace
	author map[pr.Ref]workspace
	// authorOrder are the author targets in the workspaces' order.
	authorOrder []pr.Ref
	// reviewLinked are the pull requests a review workspace links,
	// lower-cased.
	reviewLinked map[string]bool
	order        []pr.Ref
}

func (p *Planner) buildTargets() *targets {
	t := &targets{review: map[pr.Ref]workspace{}, author: map[pr.Ref]workspace{}, reviewLinked: map[string]bool{}}
	var reviewWS, authorWS []workspace
	for _, w := range p.ws {
		if w.IsMain {
			w.linked = 0
		}
		switch {
		case w.mainOnDefault():
		case !w.Local:
		case w.IsMain:
			authorWS = append(authorWS, w)
		case strings.HasPrefix(w.DisplayName, p.cfg.ReviewPrefix):
			reviewWS = append(reviewWS, w)
		default:
			authorWS = append(authorWS, w)
		}
	}
	add := func(m map[pr.Ref]workspace, ref pr.Ref, w workspace) {
		if _, ok := m[ref]; ok {
			return
		}
		m[ref] = w
		if !slices.Contains(t.order, ref) {
			t.order = append(t.order, ref)
		}
	}
	for _, w := range reviewWS {
		if w.owner == "" {
			continue
		}
		n := w.linked
		if n == 0 {
			n = p.byBranch(w.owner, w.name, strings.TrimPrefix(w.DisplayName, p.cfg.ReviewPrefix))
		}
		if n == 0 {
			continue
		}
		ref := pr.Ref{Owner: w.owner, Repo: w.name, Number: n}
		t.reviewLinked[strings.ToLower(ref.String())] = true
		add(t.review, ref, w)
	}
	for _, w := range p.ws {
		if w.owner != "" && !w.IsMain && strings.HasPrefix(w.DisplayName, p.cfg.ReviewPrefix) && w.linked != 0 {
			t.reviewLinked[strings.ToLower(pr.Ref{Owner: w.owner, Repo: w.name, Number: w.linked}.String())] = true
		}
	}
	mine := p.authored(p.in.Now.Add(-authorLookback))
	for _, w := range authorWS {
		if w.owner == "" {
			continue
		}
		ref, why := matchAuthored(mine, w)
		if ref.Number == 0 && why == "" && w.PushBranch == "" && !w.IsMain && w.Local && p.in.Git != nil {
			upstream, err := p.in.Git.Upstream(p.ctx, w.Path)
			if err != nil {
				continue
			}
			if upstream != "" && upstream != w.Branch {
				w.PushBranch = upstream
				ref, _ = matchAuthored(mine, w)
			}
		}
		if _, seen := t.author[ref]; ref.Number != 0 && !seen {
			t.authorOrder = append(t.authorOrder, ref)
			add(t.author, ref, w)
		}
	}
	return t
}

// byBranch picks, as a branch search would, the pull request of owner/name
// whose head is branch: a single one, or a single open one among several.
func (p *Planner) byBranch(owner, name, branch string) int {
	var matches []pr.PR
	for _, r := range p.in.Rows {
		if r.HeadBranch == branch && strings.EqualFold(r.Ref.Owner, owner) && strings.EqualFold(r.Ref.Repo, name) {
			matches = append(matches, r)
		}
	}
	if len(matches) == 1 {
		return matches[0].Ref.Number
	}
	n, open := 0, 0
	for _, m := range matches {
		if m.State == pr.StateOpen {
			open++
			n = m.Ref.Number
		}
	}
	if open == 1 {
		return n
	}
	return 0
}

// authored are the viewer's open pull requests and those closed or merged
// since the day of since began, open first.
func (p *Planner) authored(since time.Time) []pr.PR {
	y, m, d := since.UTC().Date()
	day := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	var open, recent []pr.PR
	for _, r := range p.in.Rows {
		switch {
		case !strings.EqualFold(r.Author, p.in.Viewer):
		case r.State == pr.StateOpen:
			open = append(open, r)
		case !r.UpdatedAt.Before(day):
			recent = append(recent, r)
		}
	}
	return append(open, recent...)
}

// matchAuthored takes the workspace's linked pull request, whoever opened
// it. By branch it needs a unique match among my pull requests, preferring
// the open one, from the same repository and not from a fork; why says
// several matched.
func matchAuthored(mine []pr.PR, w workspace) (pr.Ref, string) {
	if w.linked != 0 {
		return pr.Ref{Owner: w.owner, Repo: w.name, Number: w.linked}, ""
	}
	branch := w.branch()
	var found []pr.PR
	for _, r := range mine {
		if w.of(r.Ref) && r.HeadBranch == branch && !r.IsFork {
			found = append(found, r)
		}
	}
	switch len(found) {
	case 0:
		return pr.Ref{}, ""
	case 1:
		return found[0].Ref, ""
	}
	var open []pr.PR
	for _, r := range found {
		if r.State == pr.StateOpen {
			open = append(open, r)
		}
	}
	if len(open) == 1 {
		return open[0].Ref, ""
	}
	return pr.Ref{}, "several of my pull requests match the branch"
}
