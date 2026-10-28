package daemon

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
	"github.com/wloophq/wloop/internal/readmodel/prrow"
	"github.com/wloophq/wloop/internal/termfmt"
)

type Explorer interface {
	explore.Searcher
	IsUser(ctx context.Context, login string) (bool, error)
}

const (
	exploreAttempt    = 25 * time.Second
	exploreRetryPause = 2 * time.Second
	userLookupTimeout = 10 * time.Second
	recentMax         = 8
	metaExploreRecent = "explore_recent"
)

// handleExplore runs one search, only when the user asks, never below the
// budget the sweeps keep and never two at once.
func (s *Server) handleExplore(w http.ResponseWriter, r *http.Request) {
	if s.refusePlugin(w, unit.Unit{Provider: s.deps.Forge}) {
		return
	}
	if s.deps.Explorer == nil {
		writeError(w, http.StatusServiceUnavailable, "explore unavailable")
		return
	}
	var req api.ExploreRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	ctx := r.Context()
	if _, err := explore.Parse(ctx, req.Query, explore.Lookup{}); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.rateLimitLow() {
		rl := s.graphQLBudget()
		writeError(w, http.StatusTooManyRequests, fmt.Sprintf("GitHub budget low (%d of %d) until %s; explore waits for the reset",
			rl.Remaining, rl.Limit, rl.ResetAt.Local().Format("15:04")))
		return
	}
	if !s.exploring.TryLock() {
		writeError(w, http.StatusConflict, "a search is already running")
		return
	}
	defer s.exploring.Unlock()
	ctx = dgh.WithPriority(ctx, dgh.PriorityBackground)
	lookup, err := s.exploreLookup(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	parsed, err := explore.Parse(ctx, req.Query, lookup)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, _ := s.viewerInfo(ctx)
	viewer := s.login(id)
	if viewer != "" && strings.EqualFold(parsed.Author, viewer) {
		writeError(w, http.StatusBadRequest, "Explore leaves out your own pull requests; the My PRs view lists them")
		return
	}
	var page dgh.CandidatePage
	asked := parsed.Query
	if parsed.Ref != nil {
		asked = parsed.Ref.URL()
		sctx, cancel := context.WithTimeout(ctx, s.exploreAttempt)
		page, err = s.deps.Explorer.CandidateByRef(sctx, *parsed.Ref, viewer)
		cancel()
	} else {
		page, err = s.exploreRetry().Search(ctx, s.deps.Explorer, parsed.Query, viewer)
	}
	read := fmt.Sprintf("read %q as %s", strings.Join(strings.Fields(req.Query), " "), parsed.Read)
	hidden := ""
	if err == nil && parsed.Author != "" && page.Total == 0 && len(lookup.Repos) > 0 {
		queries := explore.AuthorInRepos(parsed.Author, lookup.Repos, parsed.Sort, parsed.Keep)
		var more dgh.CandidatePage
		var failed []error
		if more, failed, err = s.exploreRetry().AuthorInRepos(ctx, s.deps.Explorer, queries, parsed.Sort, viewer); err == nil {
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
		writeError(w, http.StatusBadGateway, "GitHub search: "+err.Error())
		return
	}
	res := api.ExploreResult{Query: asked, Read: read, Total: page.Total, Candidates: []api.Candidate{}, Cost: page.RateLimit.Cost, RateLimit: wireRateLimit(page.RateLimit)}
	if res.Candidates, err = s.wireCandidates(ctx, page.Candidates); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
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
	if res.Recent, err = s.rememberSearch(context.WithoutCancel(ctx), remembered); err != nil {
		s.logf("explore: recent searches: %v", err)
	}
	writeJSON(w, res)
}

func (s *Server) exploreRetry() explore.Retry {
	return explore.Retry{Attempt: s.exploreAttempt, Pause: s.exploreRetryPause}
}

// exploreLookup reads the repositories and owners the inventory holds, the
// owners with the most pull requests first.
func (s *Server) exploreLookup(ctx context.Context) (explore.Lookup, error) {
	prs, err := s.deps.PRs.ListPRs(ctx, false)
	if err != nil {
		return explore.Lookup{}, err
	}
	l := explore.Lookup{IsUser: s.isUser}
	count := map[string]int{}
	for _, pr := range prs {
		repo := pr.Ref.Owner + "/" + pr.Ref.Repo
		if !slices.Contains(l.Repos, repo) {
			l.Repos = append(l.Repos, repo)
		}
		if count[pr.Ref.Owner] == 0 {
			l.Owners = append(l.Owners, pr.Ref.Owner)
		}
		count[pr.Ref.Owner]++
	}
	slices.SortStableFunc(l.Owners, func(a, b string) int {
		if count[a] != count[b] {
			return count[b] - count[a]
		}
		return strings.Compare(a, b)
	})
	return l, nil
}

// isUser asks GitHub once per login for the daemon's life; a failure is not
// kept.
func (s *Server) isUser(ctx context.Context, login string) (bool, error) {
	key := strings.ToLower(login)
	if v, ok := s.users.Load(key); ok {
		return v.(bool), nil
	}
	lctx, cancel := context.WithTimeout(ctx, userLookupTimeout)
	defer cancel()
	ok, err := s.deps.Explorer.IsUser(lctx, login)
	if err != nil {
		s.logf("explore: is %s a user: %v", login, err)
		return false, err
	}
	s.users.Store(key, ok)
	return ok, nil
}

// wireCandidates wires each candidate with its brief and the actions it
// offers: the read model's row for a pull request the inventory lists, a
// row derived from the snapshot's base for any other.
func (s *Server) wireCandidates(ctx context.Context, cands []dgh.Candidate) ([]api.Candidate, error) {
	out := []api.Candidate{}
	if len(cands) == 0 {
		return out, nil
	}
	units := make([]unit.Unit, len(cands))
	for i, c := range cands {
		units[i] = c.Ref.Unit()
	}
	reads, base, at, err := s.readModel().ReadMany(ctx, units)
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
		if pr, ok := prrow.PROf(rd.In); ok {
			prs[c.Ref] = pr
		}
	}
	var d prrow.Deriver
	var overrides map[unit.Unit]dinv.Override
	if len(rows) < len(cands) {
		d = s.deriverAt(base, at)
		if overrides, err = s.deps.Overrides.Overrides(ctx); err != nil {
			return nil, err
		}
	}
	briefs := s.candidateBriefs(ctx, prs)
	for _, c := range cands {
		c.Title = termfmt.OneLine(c.Title, 300)
		c.HeadBranch = termfmt.OneLine(c.HeadBranch, 255)
		c.BaseBranch = termfmt.OneLine(c.BaseBranch, 255)
		wc := wireCandidate(c, briefs[c.Ref])
		if row, ok := rows[c.Ref]; ok {
			wc.Actions = row.Actions
		} else {
			o := overrides[c.Ref.Unit()]
			o.PR = c.Ref
			wc.Actions = d.UntrackedRow(c, o).Actions
		}
		out = append(out, wc)
	}
	return out, nil
}

// candidateBriefs are the sanitized briefs of the pull requests the
// inventory holds and has not lapsed.
func (s *Server) candidateBriefs(ctx context.Context, prs map[pr.Ref]dinv.PR) map[pr.Ref]*brief.View {
	if s.deps.BriefViews == nil {
		return nil
	}
	var live []dinv.PR
	for _, pr := range prs {
		if !pr.Lapsed() {
			live = append(live, pr)
		}
	}
	if len(live) == 0 {
		return nil
	}
	views, err := s.deps.BriefViews.Views(ctx, live...)
	if err != nil {
		s.logf("explore: briefs: %v", err)
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

func (s *Server) handleExploreRecent(w http.ResponseWriter, r *http.Request) {
	list, err := s.recentSearches(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, list)
}

func (s *Server) recentSearches(ctx context.Context) ([]string, error) {
	raw, err := s.deps.Meta.Meta(ctx, metaExploreRecent)
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
func (s *Server) rememberSearch(ctx context.Context, input string) ([]string, error) {
	list, err := s.recentSearches(ctx)
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
	return list, s.deps.Meta.SetMeta(ctx, metaExploreRecent, string(b))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
