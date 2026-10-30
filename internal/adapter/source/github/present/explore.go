package present

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brief"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
	"github.com/wloophq/wloop/internal/termfmt"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// The views of the running GitHub plugin the presenter reads.
const (
	pluginViewExplore = "explore"
	pluginViewRecent  = "explore.recent"
	pluginViewChecks  = "checks"
	// pluginViewCandidate reads one pull request from GitHub.
	pluginViewCandidate = "candidate"
)

// searched is one search as the GitHub plugin ran it.
type searched struct {
	Query      string          `json:"query"`
	Read       string          `json:"read"`
	Total      int             `json:"total"`
	Empty      string          `json:"empty"`
	RateLimit  dgh.RateLimit   `json:"rate_limit"`
	Recent     []string        `json:"recent"`
	Candidates []dgh.Candidate `json:"candidates"`
}

// explore has the GitHub plugin run one search, then wires each pull
// request it found with its brief and the actions it offers.
func (p *Presenter) explore(ctx context.Context, req api.ExploreRequest) (api.ExploreResult, error) {
	var got searched
	if err := p.pluginView(ctx, pluginViewExplore, map[string]string{"query": req.Query}, &got); err != nil {
		if errors.Is(err, src.ErrUnfit) || errors.Is(err, src.ErrUnavailable) || errors.Is(err, src.ErrNotFound) {
			return api.ExploreResult{}, err
		}
		return api.ExploreResult{}, api.Refuse(http.StatusBadGateway, "%v", err)
	}
	rl := got.RateLimit
	res := api.ExploreResult{Query: got.Query, Read: got.Read, Total: got.Total, Empty: got.Empty, Recent: got.Recent, Cost: rl.Cost,
		RateLimit: api.RateLimit{Cost: rl.Cost, Remaining: rl.Remaining, Limit: rl.Limit, ResetAt: rl.ResetAt}}
	var err error
	if res.Candidates, err = p.wireCandidates(ctx, got.Candidates); err != nil {
		return api.ExploreResult{}, err
	}
	return res, nil
}

func (p *Presenter) recentSearches(ctx context.Context) ([]string, error) {
	list := []string{}
	err := p.pluginView(ctx, pluginViewRecent, nil, &list)
	return list, err
}

// pluginView reads view of the running GitHub plugin into out.
func (p *Presenter) pluginView(ctx context.Context, view string, query, out any) error {
	if p.d.View == nil {
		return fmt.Errorf("%s: %w", view, src.ErrUnavailable)
	}
	raw, err := json.Marshal(query)
	if err != nil {
		return err
	}
	got, err := p.d.View(ctx, src.ViewRequest{View: view, Query: raw})
	if err != nil {
		return err
	}
	return json.Unmarshal(got, out)
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
