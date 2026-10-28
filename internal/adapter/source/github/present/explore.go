package present

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/brief"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/explore"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
	"github.com/wloophq/wloop/internal/termfmt"
)

const (
	userLookupTimeout = 10 * time.Second
	recentMax         = 8
	metaExploreRecent = "explore_recent"
)

// explore runs one search, only when the user asks, never below the
// budget the sweeps keep and never two at once.
func (p *Presenter) explore(ctx context.Context, req api.ExploreRequest) (api.ExploreResult, error) {
	if p.d.Explorer == nil {
		return api.ExploreResult{}, api.Refuse(http.StatusServiceUnavailable, "explore unavailable")
	}
	if _, err := explore.Parse(ctx, req.Query, explore.Lookup{}); err != nil {
		return api.ExploreResult{}, api.Refuse(http.StatusBadRequest, "%v", err)
	}
	if p.d.Budget != nil && p.d.Budget.Low() {
		rl := p.d.Budget.RateLimit()
		return api.ExploreResult{}, api.Refuse(http.StatusTooManyRequests, "GitHub budget low (%d of %d) until %s; explore waits for the reset",
			rl.Remaining, rl.Limit, rl.ResetAt.Local().Format("15:04"))
	}
	if !p.exploring.TryLock() {
		return api.ExploreResult{}, api.Refuse(http.StatusConflict, "a search is already running")
	}
	defer p.exploring.Unlock()
	ctx = dgh.WithPriority(ctx, dgh.PriorityBackground)
	lookup, err := p.exploreLookup(ctx)
	if err != nil {
		return api.ExploreResult{}, err
	}
	parsed, err := explore.Parse(ctx, req.Query, lookup)
	if err != nil {
		return api.ExploreResult{}, api.Refuse(http.StatusBadRequest, "%v", err)
	}
	viewer := p.viewer(ctx)
	if viewer != "" && strings.EqualFold(parsed.Author, viewer) {
		return api.ExploreResult{}, api.Refuse(http.StatusBadRequest, "Explore leaves out your own pull requests; the My PRs view lists them")
	}
	var page dgh.CandidatePage
	asked := parsed.Query
	if parsed.Ref != nil {
		asked = parsed.Ref.URL()
		sctx, cancel := context.WithTimeout(ctx, p.d.ExploreAttempt)
		page, err = p.d.Explorer.CandidateByRef(sctx, *parsed.Ref, viewer)
		cancel()
	} else {
		page, err = p.exploreRetry().Search(ctx, p.d.Explorer, parsed.Query, viewer)
	}
	read := fmt.Sprintf("read %q as %s", strings.Join(strings.Fields(req.Query), " "), parsed.Read)
	hidden := ""
	if err == nil && parsed.Author != "" && page.Total == 0 && len(lookup.Repos) > 0 {
		queries := explore.AuthorInRepos(parsed.Author, lookup.Repos, parsed.Sort, parsed.Keep)
		var more dgh.CandidatePage
		var failed []error
		if more, failed, err = p.exploreRetry().AuthorInRepos(ctx, p.d.Explorer, queries, parsed.Sort, viewer); err == nil {
			more.RateLimit.Cost += page.RateLimit.Cost
			page = more
		}
		n := min(len(lookup.Repos), explore.MaxFallbackRepos)
		hidden = fmt.Sprintf(" in the %s "+brand.Name+" knows", plural(n, "repository", "repositories"))
		searched := fmt.Sprintf("searched the %s "+brand.Name+" knows in %s", plural(n, "repository", "repositories"), plural(len(queries), "search", "searches"))
		if len(page.Candidates) > 0 {
			read += fmt.Sprintf("; GitHub search cannot see %s without a repository; %s", parsed.Author, searched)
		} else {
			read += "; none across GitHub, then " + searched
		}
		if len(failed) > 0 {
			read += fmt.Sprintf("; %d of the %s failed: %v", len(failed), plural(len(queries), "search", "searches"), failed[0])
		}
	}
	if err != nil {
		return api.ExploreResult{}, api.Refuse(http.StatusBadGateway, "GitHub search: %v", err)
	}
	rl := page.RateLimit
	res := api.ExploreResult{Query: asked, Read: read, Total: page.Total, Candidates: []api.Candidate{}, Cost: rl.Cost,
		RateLimit: api.RateLimit{Cost: rl.Cost, Remaining: rl.Remaining, Limit: rl.Limit, ResetAt: rl.ResetAt}}
	if res.Candidates, err = p.wireCandidates(ctx, page.Candidates); err != nil {
		return api.ExploreResult{}, err
	}
	switch {
	case len(res.Candidates) > 0:
	case parsed.Author != "":
		res.Empty = parsed.Author + " has no open pull requests you did not open" + hidden
	case parsed.Team != "":
		org, _, _ := strings.Cut(parsed.Team, "/")
		res.Empty = fmt.Sprintf("no open pull request asks %s for a review; GitHub knows a team by its slug, listed at https://github.com/orgs/%s/teams", parsed.Team, org)
	}
	remembered := strings.Join(strings.Fields(req.Query), " ")
	if parsed.Ref != nil {
		remembered = parsed.Ref.URL()
	}
	if res.Recent, err = p.rememberSearch(context.WithoutCancel(ctx), remembered); err != nil {
		p.h.Logf("explore: recent searches: %v", err)
	}
	return res, nil
}

// viewer is the viewer's GitHub login, "" when unknown.
func (p *Presenter) viewer(ctx context.Context) string {
	if p.h.Viewer == nil {
		return ""
	}
	id, _ := p.h.Viewer(ctx)
	return readmodel.Login(unit.GitHub, id)
}

func (p *Presenter) exploreRetry() explore.Retry {
	return explore.Retry{Attempt: p.d.ExploreAttempt, Pause: p.d.ExploreRetryPause}
}

// exploreLookup reads the repositories and owners the inventory holds, the
// owners with the most pull requests first.
func (p *Presenter) exploreLookup(ctx context.Context) (explore.Lookup, error) {
	prs, err := p.d.Store.ListPRs(ctx, false)
	if err != nil {
		return explore.Lookup{}, err
	}
	l := explore.Lookup{IsUser: p.isUser}
	count := map[string]int{}
	for _, x := range prs {
		repo := x.Ref.Owner + "/" + x.Ref.Repo
		if !slices.Contains(l.Repos, repo) {
			l.Repos = append(l.Repos, repo)
		}
		if count[x.Ref.Owner] == 0 {
			l.Owners = append(l.Owners, x.Ref.Owner)
		}
		count[x.Ref.Owner]++
	}
	slices.SortStableFunc(l.Owners, func(a, b string) int {
		if count[a] != count[b] {
			return count[b] - count[a]
		}
		return strings.Compare(a, b)
	})
	return l, nil
}

// isUser asks GitHub once per login for the presenter's life; a failure is
// not kept.
func (p *Presenter) isUser(ctx context.Context, login string) (bool, error) {
	key := strings.ToLower(login)
	if v, ok := p.users.Load(key); ok {
		return v.(bool), nil
	}
	lctx, cancel := context.WithTimeout(ctx, userLookupTimeout)
	defer cancel()
	ok, err := p.d.Explorer.IsUser(lctx, login)
	if err != nil {
		p.h.Logf("explore: is %s a user: %v", login, err)
		return false, err
	}
	p.users.Store(key, ok)
	return ok, nil
}

// wireCandidates wires each candidate with its brief and the actions it
// offers: the read model's row for a pull request the inventory lists, a
// row derived from the snapshot's base for any other.
func (p *Presenter) wireCandidates(ctx context.Context, cands []dgh.Candidate) ([]api.Candidate, error) {
	out := []api.Candidate{}
	if len(cands) == 0 {
		return out, nil
	}
	units := make([]unit.Unit, len(cands))
	for i, c := range cands {
		units[i] = c.Ref.Unit()
	}
	reads, base, at, err := p.h.Model.ReadMany(ctx, units)
	if err != nil {
		return nil, err
	}
	rows := map[pr.Ref]api.Row{}
	prs := map[pr.Ref]dinv.PR{}
	for _, c := range cands {
		rd, ok := reads[c.Ref.Unit()]
		if !ok {
			continue
		}
		rows[c.Ref] = rd.Row
		if x, ok := prrow.PROf(rd.In); ok {
			prs[c.Ref] = x
		}
	}
	var d prrow.Deriver
	var overrides map[pr.Ref]dinv.Override
	if len(rows) < len(cands) {
		d = p.deriverAt(base, at)
		if overrides, err = p.d.Store.Overrides(ctx); err != nil {
			return nil, err
		}
	}
	briefs := p.candidateBriefs(ctx, prs)
	for _, c := range cands {
		c = cleanCandidate(c)
		wc := wireCandidate(c, briefs[c.Ref])
		if row, ok := rows[c.Ref]; ok {
			wc.Actions = row.Actions
		} else {
			o := overrides[c.Ref]
			o.PR = c.Ref
			wc.Actions = d.UntrackedRow(c, o).Actions
		}
		out = append(out, wc)
	}
	return out, nil
}

func wireCandidate(c dgh.Candidate, b *brief.View) api.Candidate {
	return api.Candidate{Unit: c.Ref.Unit(), Label: c.Ref.String(), URL: c.URL, Title: c.Title, Author: c.Author, State: c.State, IsDraft: c.IsDraft,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt, Additions: c.Additions, Deletions: c.Deletions,
		ChangedFiles: c.ChangedFiles, HeadBranch: c.HeadBranch, BaseBranch: c.BaseBranch, ReviewDecision: c.ReviewDecision,
		ReviewRequests: c.ReviewRequests, ChecksState: c.ChecksState, Comments: c.Comments, MyReview: c.MyReview,
		MyReviewOnHead: c.MyReviewOnHead, Brief: WireBriefView(b)}
}

// candidateBriefs are the sanitized briefs of the pull requests the
// inventory holds and has not lapsed.
func (p *Presenter) candidateBriefs(ctx context.Context, prs map[pr.Ref]dinv.PR) map[pr.Ref]*brief.View {
	if p.d.Briefs == nil {
		return nil
	}
	var live []dinv.PR
	for _, x := range prs {
		if !x.Lapsed() {
			live = append(live, x)
		}
	}
	if len(live) == 0 {
		return nil
	}
	views, err := p.d.Briefs.Views(ctx, live...)
	if err != nil {
		p.h.Logf("explore: briefs: %v", err)
		return nil
	}
	out := make(map[pr.Ref]*brief.View, len(views))
	for ref, v := range views {
		if v == nil || v.Brief == nil {
			continue
		}
		clean, b := *v, *v.Brief
		b.Goal, b.Kind, b.Size, b.Risk = termfmt.OneLine(b.Goal, 300), termfmt.OneLine(b.Kind, 40), termfmt.OneLine(b.Size, 40), termfmt.OneLine(b.Risk, 40)
		clean.Brief = &b
		out[ref] = &clean
	}
	return out
}

func (p *Presenter) recentSearches(ctx context.Context) ([]string, error) {
	raw, err := p.d.Store.Meta(ctx, metaExploreRecent)
	list := []string{}
	if err != nil || raw == "" {
		return list, err
	}
	if json.Unmarshal([]byte(raw), &list) != nil {
		return []string{}, nil
	}
	return list, nil
}

// rememberSearch puts input first among the recent searches, once.
func (p *Presenter) rememberSearch(ctx context.Context, input string) ([]string, error) {
	list, err := p.recentSearches(ctx)
	if err != nil {
		return nil, err
	}
	list = slices.DeleteFunc(list, func(q string) bool { return q == input })
	list = append([]string{input}, list...)
	if len(list) > recentMax {
		list = list[:recentMax]
	}
	b, err := json.Marshal(list)
	if err != nil {
		return nil, err
	}
	return list, p.d.Store.SetMeta(ctx, metaExploreRecent, string(b))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
