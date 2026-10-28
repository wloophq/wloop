// Package github reads pull requests over GitHub's HTTPS API, batching every
// pull request of a sweep into aliased GraphQL queries.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	dgh "github.com/wloophq/wloop/internal/domain/github"
	"github.com/wloophq/wloop/internal/domain/pr"
)

type Ref = pr.Ref

func ParseURL(raw string) (Ref, error) { return pr.ParseURL(raw) }

type ReviewRequestEvent struct {
	Reviewer string
	Actor    string
	At       time.Time
}

// DraftEvent is the pull request going back to draft.
type DraftEvent struct {
	Actor string
	At    time.Time
}

type DatedCommit struct {
	Oid     string
	Parents int
	At      time.Time
}

// query returns the data by top-level alias and the error per alias.
func (c *Client) query(ctx context.Context, q string) (map[string]json.RawMessage, map[string]string, error) {
	return c.GraphQL(ctx, q, nil)
}

func lit(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

const rateLimitSelection = `rateLimit { cost remaining limit resetAt }`

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

func (n rawCandidate) candidate() (dgh.Candidate, bool) {
	owner, repo, ok := strings.Cut(n.Repository.NameWithOwner, "/")
	if n.Number == 0 || !ok {
		return dgh.Candidate{}, false
	}
	c := dgh.Candidate{Ref: Ref{Owner: owner, Repo: repo, Number: n.Number}, URL: n.URL, Title: n.Title, Author: n.Author.name(),
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
func (c *Client) SearchCandidates(ctx context.Context, q, viewer string, first int) (dgh.CandidatePage, error) {
	query := fmt.Sprintf(`query {
  %s
  search(query: %s, type: ISSUE, first: %d) { issueCount nodes { ...cand } }
}
%s`, rateLimitSelection, lit(q), first, candidateFragmentFor(viewer))
	data, aliasErrs, err := c.graphQL(ctx, query, nil, 1)
	if err != nil {
		return dgh.CandidatePage{}, err
	}
	if msg := aliasErrs["search"]; msg != "" {
		return dgh.CandidatePage{}, fmt.Errorf("graphql search: %s", msg)
	}
	if raw := data["search"]; len(raw) == 0 || string(raw) == "null" {
		return dgh.CandidatePage{}, fmt.Errorf("graphql search: no result")
	}
	var res struct {
		IssueCount int            `json:"issueCount"`
		Nodes      []rawCandidate `json:"nodes"`
	}
	if err := json.Unmarshal(data["search"], &res); err != nil {
		return dgh.CandidatePage{}, fmt.Errorf("graphql search: %w", err)
	}
	page := dgh.CandidatePage{Total: res.IssueCount, RateLimit: parseRateLimit(data["rateLimit"])}
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
func (c *Client) CandidateByRef(ctx context.Context, ref Ref, viewer string) (dgh.CandidatePage, error) {
	query := fmt.Sprintf(`query {
  %s
  pr: repository(owner: %s, name: %s) { pullRequest(number: %d) { ...cand } }
}
%s`, rateLimitSelection, lit(ref.Owner), lit(ref.Repo), ref.Number, candidateFragmentFor(viewer))
	data, aliasErrs, err := c.query(ctx, query)
	if err != nil {
		return dgh.CandidatePage{}, err
	}
	if msg := aliasErrs["pr"]; msg != "" {
		return dgh.CandidatePage{}, fmt.Errorf("%s: %s", ref, msg)
	}
	page := dgh.CandidatePage{RateLimit: parseRateLimit(data["rateLimit"])}
	var res struct {
		PullRequest *rawCandidate `json:"pullRequest"`
	}
	if raw := data["pr"]; len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &res); err != nil {
			return dgh.CandidatePage{}, fmt.Errorf("%s: %w", ref, err)
		}
	}
	if res.PullRequest != nil {
		if cand, ok := res.PullRequest.candidate(); ok {
			page.Candidates, page.Total = []dgh.Candidate{cand}, 1
		}
	}
	return page, nil
}

type rawActor struct {
	Typename string `json:"__typename"`
	Login    string `json:"login"`
}

// name carries the "[bot]" suffix REST gives bot logins and GraphQL leaves
// out, so bots read the same whichever API saw them.
func (a *rawActor) name() string {
	if a == nil {
		return ""
	}
	if a.Typename == "Bot" && a.Login != "" && !strings.HasSuffix(a.Login, "[bot]") {
		return a.Login + "[bot]"
	}
	return a.Login
}

type rawReviewer struct {
	Typename     string `json:"__typename"`
	Login        string `json:"login"`
	CombinedSlug string `json:"combinedSlug"`
}

func reviewerName(a rawReviewer) string {
	switch {
	case a.Login != "":
		return (&rawActor{Typename: a.Typename, Login: a.Login}).name()
	case a.CombinedSlug != "":
		return "team:" + a.CombinedSlug
	}
	return ""
}

type rawReview struct {
	ID              string     `json:"id"`
	State           string     `json:"state"`
	SubmittedAt     *time.Time `json:"submittedAt"`
	Author          *rawActor  `json:"author"`
	ViewerDidAuthor bool       `json:"viewerDidAuthor"`
	Commit          *struct {
		Oid string `json:"oid"`
	} `json:"commit"`
	Comments *struct {
		TotalCount int `json:"totalCount"`
	} `json:"comments"`
}

func (r rawReview) review() pr.Review {
	out := pr.Review{ID: r.ID, State: r.State, HasContent: r.State == pr.ReviewCommented || r.State == pr.ReviewChangesRequested || (r.Comments != nil && r.Comments.TotalCount > 0)}
	if r.SubmittedAt != nil {
		out.SubmittedAt = *r.SubmittedAt
	}
	out.Author = r.Author.name()
	out.ByViewer = r.ViewerDidAuthor
	if r.Commit != nil {
		out.CommitOid = r.Commit.Oid
	}
	return out
}

type rawPR struct {
	Number            int        `json:"number"`
	URL               string     `json:"url"`
	CreatedAt         *time.Time `json:"createdAt"`
	UpdatedAt         *time.Time `json:"updatedAt"`
	MergedAt          *time.Time `json:"mergedAt"`
	ClosedAt          *time.Time `json:"closedAt"`
	LastEditedAt      *time.Time `json:"lastEditedAt"`
	ChangedFiles      int        `json:"changedFiles"`
	Mergeable         string     `json:"mergeable"`
	Title             string     `json:"title"`
	State             string     `json:"state"`
	IsDraft           bool       `json:"isDraft"`
	HeadRefName       string     `json:"headRefName"`
	BaseRefName       string     `json:"baseRefName"`
	HeadRefOid        string     `json:"headRefOid"`
	IsCrossRepository bool       `json:"isCrossRepository"`
	AuthorAssociation string     `json:"authorAssociation"`
	ReviewDecision    string     `json:"reviewDecision"`
	MergedBy          *rawActor  `json:"mergedBy"`
	MergeCommit       *struct {
		Oid string `json:"oid"`
	} `json:"mergeCommit"`
	Labels struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
		Visibility    string `json:"visibility"`
	} `json:"repository"`
	HeadRepositoryOwner *rawActor `json:"headRepositoryOwner"`
	Author              *rawActor `json:"author"`
	Reviews             struct {
		Nodes []rawReview `json:"nodes"`
	} `json:"reviews"`
	LatestReviews struct {
		Nodes []rawReview `json:"nodes"`
	} `json:"latestReviews"`
	ChangesRequested struct {
		Nodes []rawReview `json:"nodes"`
	} `json:"changesRequested"`
	Comments struct {
		Nodes []struct {
			ID        string    `json:"id"`
			CreatedAt time.Time `json:"createdAt"`
			Author    *rawActor `json:"author"`
		} `json:"nodes"`
	} `json:"comments"`
	ReviewRequests struct {
		Nodes []struct {
			RequestedReviewer rawReviewer `json:"requestedReviewer"`
		} `json:"nodes"`
	} `json:"reviewRequests"`
	Commits struct {
		Nodes []struct {
			Commit struct {
				Oid           string    `json:"oid"`
				CommittedDate time.Time `json:"committedDate"`
				Parents       struct {
					TotalCount int `json:"totalCount"`
				} `json:"parents"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
	TimelineItems struct {
		FilteredCount int `json:"filteredCount"`
		Nodes         []struct {
			Typename          string      `json:"__typename"`
			CreatedAt         time.Time   `json:"createdAt"`
			Actor             *rawActor   `json:"actor"`
			RequestedReviewer rawReviewer `json:"requestedReviewer"`
		} `json:"nodes"`
	} `json:"timelineItems"`
	ReviewThreads struct {
		TotalCount int `json:"totalCount"`
		Nodes      []struct {
			IsResolved bool `json:"isResolved"`
			Comments   struct {
				Nodes []struct {
					CreatedAt time.Time `json:"createdAt"`
					Author    *rawActor `json:"author"`
				} `json:"nodes"`
			} `json:"comments"`
		} `json:"nodes"`
	} `json:"reviewThreads"`
	HeadCommit struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					State    string `json:"state"`
					Contexts struct {
						Nodes []rawCheck `json:"nodes"`
					} `json:"contexts"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"headCommit"`
}

type rawCheck struct {
	Name        string  `json:"name"`
	Status      string  `json:"status"`
	Conclusion  string  `json:"conclusion"`
	Title       *string `json:"title"`
	DetailsURL  string  `json:"detailsUrl"`
	Context     string  `json:"context"`
	State       string  `json:"state"`
	Description string  `json:"description"`
	TargetURL   *string `json:"targetUrl"`
}

func (r rawCheck) check() pr.Check {
	if r.Context != "" {
		return pr.Check{
			Name:    r.Context,
			Pending: r.State == "PENDING" || r.State == "EXPECTED",
			Failed:  r.State == "FAILURE" || r.State == "ERROR",
			URL:     deref(r.TargetURL),
			Summary: r.Description,
		}
	}
	c := pr.Check{Name: r.Name, URL: r.DetailsURL, Summary: deref(r.Title)}
	switch r.Conclusion {
	case "FAILURE", "TIMED_OUT", "STARTUP_FAILURE", "ACTION_REQUIRED":
		c.Failed = true
	default:
		c.Pending = r.Status != "COMPLETED"
	}
	return c
}

func (r rawPR) pullRequest() *pr.PullRequest {
	owner, repo, _ := strings.Cut(r.Repository.NameWithOwner, "/")
	out := &pr.PullRequest{Facts: pr.Facts{
		Ref:               Ref{Owner: owner, Repo: repo, Number: r.Number},
		State:             r.State,
		IsDraft:           r.IsDraft,
		Title:             r.Title,
		HeadRefName:       r.HeadRefName,
		BaseRefName:       r.BaseRefName,
		HeadRefOid:        r.HeadRefOid,
		IsCrossRepository: r.IsCrossRepository,
		Visibility:        strings.ToLower(r.Repository.Visibility),
		AuthorAssociation: r.AuthorAssociation,
		ReviewDecision:    r.ReviewDecision,
		Mergeable:         r.Mergeable,
	}}
	if r.HeadRepositoryOwner != nil {
		out.HeadRepositoryOwner = r.HeadRepositoryOwner.Login
	}
	out.Author = r.Author.name()
	for _, n := range r.Reviews.Nodes {
		out.Reviews = append(out.Reviews, n.review())
	}
	out.ThreadsTotal = r.ReviewThreads.TotalCount
	for _, t := range r.ReviewThreads.Nodes {
		if t.IsResolved || len(t.Comments.Nodes) == 0 {
			continue
		}
		last := t.Comments.Nodes[len(t.Comments.Nodes)-1]
		out.OpenThreads = append(out.OpenThreads, pr.Thread{LastAuthor: last.Author.name(), LastAt: last.CreatedAt})
	}
	for _, n := range r.LatestReviews.Nodes {
		out.LatestReviews = append(out.LatestReviews, n.review())
	}
	if len(r.ChangesRequested.Nodes) > 0 {
		out.NewestChangesRequestOid = r.ChangesRequested.Nodes[len(r.ChangesRequested.Nodes)-1].review().CommitOid
	}
	for _, n := range r.Comments.Nodes {
		out.Comments = append(out.Comments, pr.Comment{ID: n.ID, Author: n.Author.name(), CreatedAt: n.CreatedAt})
	}
	for _, n := range r.ReviewRequests.Nodes {
		if name := reviewerName(n.RequestedReviewer); name != "" {
			out.ReviewRequests = append(out.ReviewRequests, name)
		}
	}
	for _, n := range r.Commits.Nodes {
		out.Commits = append(out.Commits, pr.Commit{Oid: n.Commit.Oid, Parents: n.Commit.Parents.TotalCount})
	}
	for _, n := range r.Labels.Nodes {
		out.Labels = append(out.Labels, n.Name)
	}
	if len(r.HeadCommit.Nodes) > 0 && r.HeadCommit.Nodes[0].Commit.StatusCheckRollup != nil {
		rollup := r.HeadCommit.Nodes[0].Commit.StatusCheckRollup
		out.ChecksState = rollup.State
		for _, n := range rollup.Contexts.Nodes {
			if n.Name != "" || n.Context != "" {
				out.Checks = append(out.Checks, n.check())
			}
		}
	}
	return out
}

func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

type rawRateLimit struct {
	Cost      int       `json:"cost"`
	Remaining int       `json:"remaining"`
	Limit     int       `json:"limit"`
	ResetAt   time.Time `json:"resetAt"`
}

func parseRateLimit(raw json.RawMessage) dgh.RateLimit {
	var r rawRateLimit
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &r)
	}
	return dgh.RateLimit(r)
}

func (c *Client) Viewer(ctx context.Context) (string, error) {
	data, _, err := c.query(ctx, "query { viewer { login } "+rateLimitSelection+" }")
	if err != nil {
		return "", err
	}
	return viewerLogin(data)
}

func viewerLogin(data map[string]json.RawMessage) (string, error) {
	var v struct {
		Login string `json:"login"`
	}
	if err := json.Unmarshal(data["viewer"], &v); err != nil || v.Login == "" {
		return "", fmt.Errorf("graphql: no viewer login")
	}
	return v.Login, nil
}

type rawPageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

func (c *Client) TeamMembers(ctx context.Context, org, slug string) ([]string, error) {
	var members []string
	cursor := "null"
	for {
		q := fmt.Sprintf(`query {
  %s
  organization(login: %s) { team(slug: %s) { members(first: 100, after: %s) { pageInfo { hasNextPage endCursor } nodes { login } } } }
}`, rateLimitSelection, lit(org), lit(slug), cursor)
		data, aliasErrs, err := c.query(ctx, q)
		if err != nil {
			return nil, err
		}
		var res struct {
			Team *struct {
				Members struct {
					PageInfo rawPageInfo `json:"pageInfo"`
					Nodes    []rawActor  `json:"nodes"`
				} `json:"members"`
			} `json:"team"`
		}
		if raw := data["organization"]; len(raw) > 0 && string(raw) != "null" {
			if err := json.Unmarshal(raw, &res); err != nil {
				return nil, fmt.Errorf("graphql team %s/%s: %w", org, slug, err)
			}
		}
		if res.Team == nil {
			msg := aliasErrs["organization"]
			if msg == "" {
				msg = "not found"
			}
			return nil, fmt.Errorf("team %s/%s: %s", org, slug, msg)
		}
		for _, n := range res.Team.Members.Nodes {
			if n.Login != "" {
				members = append(members, n.Login)
			}
		}
		if !res.Team.Members.PageInfo.HasNextPage {
			return members, nil
		}
		cursor = lit(res.Team.Members.PageInfo.EndCursor)
	}
}

type BranchMatch struct {
	Number int
	State  string
}

// PickByBranch accepts a single match, or a single open one among several:
// branch names repeat across time, and a guess could revive a finished
// review.
func PickByBranch(matches []BranchMatch) (int, string) {
	if len(matches) == 0 {
		return 0, "no pull request found for the branch"
	}
	if len(matches) == 1 {
		return matches[0].Number, ""
	}
	open := 0
	n := 0
	for _, m := range matches {
		if m.State == dgh.StateOpen {
			open++
			n = m.Number
		}
	}
	if open == 1 {
		return n, ""
	}
	return 0, fmt.Sprintf("%d pull requests match the branch", len(matches))
}

func (c *Client) Basics(ctx context.Context, r Ref) (*pr.PullRequest, error) {
	q := fmt.Sprintf(`query {
  viewer { login }
  %s
  pr: repository(owner: %s, name: %s) { pullRequest(number: %d) { number title state headRefName baseRefName headRefOid isCrossRepository authorAssociation repository { nameWithOwner visibility } author { __typename login } reviews(last: 100) { nodes { state submittedAt viewerDidAuthor commit { oid } } } reviewRequests(first: 20) { nodes { requestedReviewer { __typename ... on User { login } ... on Bot { login } ... on Team { combinedSlug } } } } labels(first: 50) { nodes { name } } } }
}`, rateLimitSelection, lit(r.Owner), lit(r.Repo), r.Number)
	data, aliasErrs, err := c.query(ctx, q)
	if err != nil {
		return nil, err
	}
	var repo struct {
		PullRequest *rawPR `json:"pullRequest"`
	}
	if raw := data["pr"]; len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &repo); err != nil {
			return nil, err
		}
	}
	if repo.PullRequest == nil {
		msg := aliasErrs["pr"]
		if msg == "" {
			msg = "not found"
		}
		return nil, fmt.Errorf("%s: %s", r, msg)
	}
	pr := repo.PullRequest.pullRequest()
	if viewer, err := viewerLogin(data); err == nil {
		pr.ViewerRequested = slices.ContainsFunc(pr.ReviewRequests, func(l string) bool { return strings.EqualFold(l, viewer) })
	}
	return pr, nil
}
