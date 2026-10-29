package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/wloophq/wloop/plugins/github/pr"
)

// Candidate is a pull request Explore found, with what deciding whether to
// help on it needs.
type Candidate struct {
	Ref            pr.Ref    `json:"ref"`
	URL            string    `json:"url"`
	Title          string    `json:"title"`
	Author         string    `json:"author"`
	State          string    `json:"state"`
	IsDraft        bool      `json:"draft"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	Additions      int       `json:"additions"`
	Deletions      int       `json:"deletions"`
	ChangedFiles   int       `json:"changed_files"`
	HeadBranch     string    `json:"head"`
	BaseBranch     string    `json:"base"`
	ReviewDecision string    `json:"review_decision"`
	ReviewRequests []string  `json:"review_requests"` // logins and team:org/slug
	ChecksState    string    `json:"checks"`
	Comments       int       `json:"comments"`
	// MyReview is the viewer's latest review state, "" when none.
	MyReview       string `json:"my_review"`
	MyReviewOnHead bool   `json:"my_review_on_head"`
}

type CandidatePage struct {
	Candidates []Candidate
	Total      int
	RateLimit  RateLimit
}

const candidateFragment = `fragment cand on PullRequest {
  number title url state isDraft createdAt updatedAt additions deletions changedFiles
  headRefName baseRefName reviewDecision
  repository { nameWithOwner }
  author { __typename login }
  reviewRequests(first: 10) { nodes { requestedReviewer { __typename ... on User { login } ... on Bot { login } ... on Team { combinedSlug } } } }
  comments { totalCount }
  %s
  headCommit: commits(last: 1) { nodes { commit { oid statusCheckRollup { state } } } }
}`

func candidateFragmentFor(viewer string) string {
	mine := ""
	if viewer != "" {
		mine = fmt.Sprintf("mine: reviews(last: 1, author: %s) { nodes { state commit { oid } } }", lit(viewer))
	}
	return fmt.Sprintf(candidateFragment, mine)
}

type rawCandidate struct {
	Number         int       `json:"number"`
	Title          string    `json:"title"`
	URL            string    `json:"url"`
	State          string    `json:"state"`
	IsDraft        bool      `json:"isDraft"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
	Additions      int       `json:"additions"`
	Deletions      int       `json:"deletions"`
	ChangedFiles   int       `json:"changedFiles"`
	HeadRefName    string    `json:"headRefName"`
	BaseRefName    string    `json:"baseRefName"`
	ReviewDecision string    `json:"reviewDecision"`
	Repository     struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	Author         *rawActor `json:"author"`
	ReviewRequests struct {
		Nodes []struct {
			RequestedReviewer rawReviewer `json:"requestedReviewer"`
		} `json:"nodes"`
	} `json:"reviewRequests"`
	Comments struct {
		TotalCount int `json:"totalCount"`
	} `json:"comments"`
	Mine struct {
		Nodes []struct {
			State  string `json:"state"`
			Commit *struct {
				Oid string `json:"oid"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"mine"`
	HeadCommit struct {
		Nodes []struct {
			Commit struct {
				Oid               string `json:"oid"`
				StatusCheckRollup *struct {
					State string `json:"state"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"headCommit"`
}

func (n rawCandidate) candidate() (Candidate, bool) {
	owner, repo, ok := strings.Cut(n.Repository.NameWithOwner, "/")
	if n.Number == 0 || !ok {
		return Candidate{}, false
	}
	c := Candidate{Ref: Ref{Owner: owner, Repo: repo, Number: n.Number}, URL: n.URL, Title: n.Title, Author: n.Author.name(),
		State: n.State, IsDraft: n.IsDraft, CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt, Additions: n.Additions,
		Deletions: n.Deletions, ChangedFiles: n.ChangedFiles, HeadBranch: n.HeadRefName, BaseBranch: n.BaseRefName,
		ReviewDecision: n.ReviewDecision, ReviewRequests: []string{}, Comments: n.Comments.TotalCount}
	for _, rr := range n.ReviewRequests.Nodes {
		if name := reviewerName(rr.RequestedReviewer); name != "" {
			c.ReviewRequests = append(c.ReviewRequests, name)
		}
	}
	head := ""
	if len(n.HeadCommit.Nodes) > 0 {
		hc := n.HeadCommit.Nodes[0].Commit
		head = hc.Oid
		if hc.StatusCheckRollup != nil {
			c.ChecksState = hc.StatusCheckRollup.State
		}
	}
	if len(n.Mine.Nodes) > 0 {
		m := n.Mine.Nodes[0]
		c.MyReview = m.State
		c.MyReviewOnHead = head != "" && m.Commit != nil && m.Commit.Oid == head
	}
	return c, true
}

// SearchCandidates reads the first page of a pull request search; viewer
// names whose review is "mine", "" asks for none. It does not retry: the
// caller pauses before it does.
func (c *Client) SearchCandidates(ctx context.Context, q, viewer string, first int) (CandidatePage, error) {
	query := fmt.Sprintf(`query {
  %s
  search(query: %s, type: ISSUE, first: %d) { issueCount nodes { ...cand } }
}
%s`, rateLimitSelection, lit(q), first, candidateFragmentFor(viewer))
	data, aliasErrs, err := c.graphQL(ctx, query, nil, 1)
	if err != nil {
		return CandidatePage{}, err
	}
	if msg := aliasErrs["search"]; msg != "" {
		return CandidatePage{}, fmt.Errorf("graphql search: %s", msg)
	}
	if raw := data["search"]; len(raw) == 0 || string(raw) == "null" {
		return CandidatePage{}, fmt.Errorf("graphql search: no result")
	}
	var res struct {
		IssueCount int            `json:"issueCount"`
		Nodes      []rawCandidate `json:"nodes"`
	}
	if err := json.Unmarshal(data["search"], &res); err != nil {
		return CandidatePage{}, fmt.Errorf("graphql search: %w", err)
	}
	page := CandidatePage{Total: res.IssueCount, RateLimit: parseRateLimit(data["rateLimit"])}
	for _, n := range res.Nodes {
		if cand, ok := n.candidate(); ok {
			page.Candidates = append(page.Candidates, cand)
		}
	}
	return page, nil
}

var loginRE = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9]|-[A-Za-z0-9]){0,38}$`)

func ValidLogin(s string) bool { return loginRE.MatchString(s) }

// IsUser reports whether login names a GitHub user; an organization or an
// unknown login is not one.
func (c *Client) IsUser(ctx context.Context, login string) (bool, error) {
	if !ValidLogin(login) {
		return false, fmt.Errorf("%q is not a GitHub login", login)
	}
	resp, err := c.REST(ctx, Request{Path: "users/" + login})
	if err != nil {
		if se := (*StatusError)(nil); errors.As(err, &se) && se.Status == http.StatusNotFound {
			return false, nil
		}
		return false, err
	}
	var user struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(resp.Body, &user); err != nil {
		return false, fmt.Errorf("users/%s: %w", login, err)
	}
	return user.Type == "User", nil
}

// CandidateByRef reads one pull request as a candidate, in any state.
func (c *Client) CandidateByRef(ctx context.Context, ref Ref, viewer string) (CandidatePage, error) {
	query := fmt.Sprintf(`query {
  %s
  pr: repository(owner: %s, name: %s) { pullRequest(number: %d) { ...cand } }
}
%s`, rateLimitSelection, lit(ref.Owner), lit(ref.Repo), ref.Number, candidateFragmentFor(viewer))
	data, aliasErrs, err := c.query(ctx, query)
	if err != nil {
		return CandidatePage{}, err
	}
	if msg := aliasErrs["pr"]; msg != "" {
		return CandidatePage{}, fmt.Errorf("%s: %s", ref, msg)
	}
	page := CandidatePage{RateLimit: parseRateLimit(data["rateLimit"])}
	var res struct {
		PullRequest *rawCandidate `json:"pullRequest"`
	}
	if raw := data["pr"]; len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &res); err != nil {
			return CandidatePage{}, fmt.Errorf("%s: %w", ref, err)
		}
	}
	if res.PullRequest != nil {
		if cand, ok := res.PullRequest.candidate(); ok {
			page.Candidates, page.Total = []Candidate{cand}, 1
		}
	}
	return page, nil
}

// SearchesPerRequest bounds the searches one SearchCandidatesEach request
// carries: GitHub refuses a request that reads 100 candidates.
const SearchesPerRequest = 3

// SearchCandidatesEach reads the first page of each search in qs as aliases
// of one GraphQL request, the pages in the order of qs; every page carries
// the request's rate limit, and errs holds, per query, the error of that
// search alone. It does not retry: the caller pauses before it does.
func (c *Client) SearchCandidatesEach(ctx context.Context, qs []string, viewer string, first int) (pages []CandidatePage, errs []error, err error) {
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
	pages, errs = make([]CandidatePage, len(qs)), make([]error, len(qs))
	for i, q := range qs {
		pages[i], errs[i] = candidatesOf(data[fmt.Sprintf("s%d", i)], aliasErrs[fmt.Sprintf("s%d", i)], rl)
		if errs[i] != nil {
			errs[i] = fmt.Errorf("graphql search %q: %w", q, errs[i])
		}
	}
	return pages, errs, nil
}

func candidatesOf(raw json.RawMessage, aliasErr string, rl RateLimit) (CandidatePage, error) {
	page := CandidatePage{RateLimit: rl}
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
