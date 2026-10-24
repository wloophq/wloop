package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	"strings"
)

// SearchesPerRequest bounds the searches one SearchCandidatesEach request
// carries: GitHub refuses a request that reads 100 candidates.
const SearchesPerRequest = 3

// SearchCandidatesEach reads the first page of each search in qs as aliases
// of one GraphQL request, the pages in the order of qs; every page carries
// the request's rate limit, and errs holds, per query, the error of that
// search alone. It does not retry: the caller pauses before it does.
func (c *Client) SearchCandidatesEach(ctx context.Context, qs []string, viewer string, first int) (pages []dgh.CandidatePage, errs []error, err error) {
	if len(qs) == 0 {
		return nil, nil, nil
	}
	var sel strings.Builder
	for i, q := range qs {
		fmt.Fprintf(&sel, "  s%d: search(query: %s, type: ISSUE, first: %d) { issueCount nodes { ...cand } }\n", i, lit(q), first)
	}
	query := fmt.Sprintf("query {\n  %s\n%s}\n%s", rateLimitSelection, sel.String(), candidateFragmentFor(viewer))
	data, aliasErrs, err := c.graphQL(ctx, query, nil, 1)
	if err != nil {
		return nil, nil, err
	}
	rl := parseRateLimit(data["rateLimit"])
	pages, errs = make([]dgh.CandidatePage, len(qs)), make([]error, len(qs))
	for i, q := range qs {
		pages[i], errs[i] = candidatesOf(data[fmt.Sprintf("s%d", i)], aliasErrs[fmt.Sprintf("s%d", i)], rl)
		if errs[i] != nil {
			errs[i] = fmt.Errorf("graphql search %q: %w", q, errs[i])
		}
	}
	return pages, errs, nil
}

func candidatesOf(raw json.RawMessage, aliasErr string, rl dgh.RateLimit) (dgh.CandidatePage, error) {
	page := dgh.CandidatePage{RateLimit: rl}
	if aliasErr != "" {
		return page, errors.New(aliasErr)
	}
	if len(raw) == 0 || string(raw) == "null" {
		return page, errors.New("no result")
	}
	var res struct {
		IssueCount int            `json:"issueCount"`
		Nodes      []rawCandidate `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return page, err
	}
	page.Total = res.IssueCount
	for _, n := range res.Nodes {
		if cand, ok := n.candidate(); ok {
			page.Candidates = append(page.Candidates, cand)
		}
	}
	return page, nil
}
