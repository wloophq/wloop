// Package github reads pull requests over GitHub's HTTPS API, batching every
// pull request of a sweep into aliased GraphQL queries.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	dgh "github.com/wloophq/wloop/internal/domain/github"
	"github.com/wloophq/wloop/internal/domain/pr"
)

const reviewPending = "PENDING"

type Ref = pr.Ref

func ParseURL(raw string) (Ref, error) { return pr.ParseURL(raw) }

// PullRequest is a pull request as a read returned it: the facts the decide
// rules read and the head repository's owner.
type PullRequest struct {
	pr.Facts
	HeadRepositoryOwner string
}

// Snapshot is one pull request as the inventory sees it.
type Snapshot struct {
	PullRequest
	URL            string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	MergedAt       time.Time
	ClosedAt       time.Time
	LastEditedAt   time.Time // last edit of the description; zero when never edited
	ChangedFiles   int
	ReadyAt        time.Time
	ReviewRequests []ReviewRequestEvent
	CommitsDated   []DatedCommit
	MergedBy       string
	MergeCommit    string
	Drafts         []DraftEvent
	RemovedReviews []ReviewRequestEvent
	// TimelineMissed counts the earliest timeline events the read left out.
	TimelineMissed int
	// ThreadsRead says OpenThreads is known: Threads read it, or there are
	// no threads. A full read only counts them.
	ThreadsRead bool
}

// Stub is what the cheap search or by-ref read returns: enough to decide
// whether to read the full snapshot.
type Stub struct {
	Ref            Ref
	Author         string
	UpdatedAt      time.Time
	IsDraft        bool
	HeadOid        string
	ReviewDecision string
	ReviewRequests []string // logins and team:org/slug
	NameWithOwner  string
	// ChecksState is the head commit's status check rollup, as in Snapshot:
	// checks concluding do not move UpdatedAt.
	ChecksState string
	// FailedChecks are the names of the head's failed checks, sorted: a
	// check can fail or recover without moving UpdatedAt or the rollup.
	FailedChecks []string
	State        string // OPEN, CLOSED or MERGED
	// Mergeable, as in PullRequest: a base that moves leaves UpdatedAt.
	Mergeable string
}

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

type StubPage struct {
	Stubs     []Stub
	HasNext   bool
	EndCursor string
	RateLimit dgh.RateLimit
}

// query returns the data by top-level alias and the error per alias.
func (c *Client) query(ctx context.Context, q string) (map[string]json.RawMessage, map[string]string, error) {
	return c.GraphQL(ctx, q, nil)
}

func lit(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

const stubFragment = `fragment stub on PullRequest {
  number state isDraft updatedAt mergeable headRefOid reviewDecision repository { nameWithOwner } author { __typename login }
  reviewRequests(first: 20) { nodes { requestedReviewer { __typename ... on User { login } ... on Bot { login } ... on Team { combinedSlug } } } }
  headCommit: commits(last: 1) { nodes { commit { statusCheckRollup { state contexts(first: 50) { nodes {
    ... on CheckRun { name status conclusion }
    ... on StatusContext { context state }
  } } } } } }
}`

const inventoryFragment = `fragment inv on PullRequest {
  number title state isDraft url createdAt updatedAt mergedAt closedAt lastEditedAt changedFiles mergeable
  mergedBy { __typename login } mergeCommit { oid }
  headRefName baseRefName headRefOid isCrossRepository
  repository { nameWithOwner visibility }
  author { __typename login } authorAssociation reviewDecision
  headRepositoryOwner { login }
  labels(first: 20) { nodes { name } }
  commits(last: 50) { nodes { commit { oid committedDate parents { totalCount } } } }
  reviews(last: 100) { nodes { id state submittedAt author { __typename login } commit { oid } comments { totalCount } } }
  latestReviews(first: 20) { nodes { id state submittedAt author { __typename login } commit { oid } } }
  changesRequested: reviews(last: 1, states: [CHANGES_REQUESTED]) { nodes { id state submittedAt author { __typename login } commit { oid } } }
  reviewThreads { totalCount }
  comments(last: 100) { nodes { id createdAt author { __typename login } } }
  reviewRequests(first: 20) { nodes { requestedReviewer { __typename ... on User { login } ... on Bot { login } ... on Team { combinedSlug } } } }
  timelineItems(last: 100, itemTypes: [REVIEW_REQUESTED_EVENT, REVIEW_REQUEST_REMOVED_EVENT, READY_FOR_REVIEW_EVENT, CONVERT_TO_DRAFT_EVENT]) { filteredCount nodes {
    __typename
    ... on ReviewRequestedEvent { createdAt actor { __typename login } requestedReviewer { __typename ... on User { login } ... on Bot { login } ... on Team { combinedSlug } } }
    ... on ReviewRequestRemovedEvent { createdAt actor { __typename login } requestedReviewer { __typename ... on User { login } ... on Bot { login } ... on Team { combinedSlug } } }
    ... on ReadyForReviewEvent { createdAt }
    ... on ConvertToDraftEvent { createdAt actor { __typename login } }
  } }
  headCommit: commits(last: 1) { nodes { commit { oid statusCheckRollup { state contexts(first: 50) { nodes {
    ... on CheckRun { name status conclusion title detailsUrl }
    ... on StatusContext { context state description targetUrl }
  } } } } } }
}`

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

func (r rawPR) pullRequest() *PullRequest {
	owner, repo, _ := strings.Cut(r.Repository.NameWithOwner, "/")
	out := &PullRequest{Facts: pr.Facts{
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

func (r rawPR) snapshot() Snapshot {
	s := Snapshot{PullRequest: *r.pullRequest(), URL: r.URL, CreatedAt: deref(r.CreatedAt), UpdatedAt: deref(r.UpdatedAt), MergedAt: deref(r.MergedAt), ClosedAt: deref(r.ClosedAt),
		LastEditedAt: deref(r.LastEditedAt), ChangedFiles: r.ChangedFiles, MergedBy: r.MergedBy.name()}
	if r.MergeCommit != nil {
		s.MergeCommit = r.MergeCommit.Oid
	}
	// Only the viewer sees its own pending review, and it is not submitted yet.
	s.Reviews = slices.DeleteFunc(s.Reviews, func(rv pr.Review) bool { return rv.State == reviewPending })
	s.ChecksState = rollupState(s.ChecksState)
	for _, n := range r.Commits.Nodes {
		s.CommitsDated = append(s.CommitsDated, DatedCommit{Oid: n.Commit.Oid, Parents: n.Commit.Parents.TotalCount, At: n.Commit.CommittedDate})
	}
	s.TimelineMissed = max(0, r.TimelineItems.FilteredCount-len(r.TimelineItems.Nodes))
	for _, n := range r.TimelineItems.Nodes {
		switch n.Typename {
		case "ReviewRequestedEvent":
			if name := reviewerName(n.RequestedReviewer); name != "" {
				s.ReviewRequests = append(s.ReviewRequests, ReviewRequestEvent{Reviewer: name, Actor: n.Actor.name(), At: n.CreatedAt})
			}
		case "ReviewRequestRemovedEvent":
			if name := reviewerName(n.RequestedReviewer); name != "" {
				s.RemovedReviews = append(s.RemovedReviews, ReviewRequestEvent{Reviewer: name, Actor: n.Actor.name(), At: n.CreatedAt})
			}
		case "ReadyForReviewEvent":
			s.ReadyAt = n.CreatedAt
		case "ConvertToDraftEvent":
			s.Drafts = append(s.Drafts, DraftEvent{Actor: n.Actor.name(), At: n.CreatedAt})
		}
	}
	return s
}

func rollupState(state string) string {
	switch state {
	case "ERROR":
		return "FAILURE"
	case "EXPECTED":
		return "PENDING"
	}
	return state
}

func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

const batchSize = 20

// InventoryByRef reads full snapshots by reference. The rate limit adds up
// the cost of every batch and keeps the budget the last one reported.
func (c *Client) InventoryByRef(ctx context.Context, refs []Ref) (map[Ref]Snapshot, map[Ref]error, dgh.RateLimit, error) {
	snaps := map[Ref]Snapshot{}
	if len(refs) == 0 {
		return snaps, map[Ref]error{}, dgh.RateLimit{}, nil
	}
	rl, errs, err := c.byRef(ctx, refs, batchSize, "inv", inventoryFragment, decodePR(func(r Ref, raw *rawPR) { snaps[r] = raw.snapshot() }))
	if err != nil {
		return nil, nil, dgh.RateLimit{}, err
	}
	return snaps, errs, rl, nil
}

// MaxThreads caps the review threads Threads reads of one pull request.
const MaxThreads = 50

// Threads reads the unresolved threads among the newest refs[ref] review
// threads of each ref, at most MaxThreads, in batches of 20 aliases.
// Per-ref failures are in errs; a failed request is err, returned with
// what the earlier requests read.
func (c *Client) Threads(ctx context.Context, refs map[Ref]int) (threads map[Ref][]pr.Thread, errs map[Ref]error, err error) {
	threads, errs = map[Ref][]pr.Thread{}, map[Ref]error{}
	sorted := slices.SortedFunc(maps.Keys(refs), func(a, b Ref) int { return strings.Compare(a.String(), b.String()) })
	for chunk := range slices.Chunk(sorted, batchSize) {
		var b strings.Builder
		b.WriteString("query {\n  " + rateLimitSelection + "\n")
		for i, r := range chunk {
			n := min(max(refs[r], 1), MaxThreads)
			fmt.Fprintf(&b, "  pr%d: repository(owner: %s, name: %s) { pullRequest(number: %d) { reviewThreads(last: %d) { nodes { isResolved comments(last: 1) { nodes { createdAt author { __typename login } } } } } } }\n",
				i, lit(r.Owner), lit(r.Repo), r.Number, n)
		}
		b.WriteString("}\n")
		attempts := 1
		if len(chunk) == 1 {
			attempts = c.retry.Attempts
		}
		data, aliasErrs, err := c.graphQL(ctx, b.String(), nil, attempts)
		if err != nil {
			return threads, errs, err
		}
		for i, r := range chunk {
			alias := fmt.Sprintf("pr%d", i)
			var repo struct {
				PullRequest *rawPR `json:"pullRequest"`
			}
			if raw := data[alias]; len(raw) > 0 && string(raw) != "null" {
				if err := json.Unmarshal(raw, &repo); err != nil {
					errs[r] = fmt.Errorf("%s: %w", r, err)
					continue
				}
			}
			if repo.PullRequest == nil {
				msg := aliasErrs[alias]
				if msg == "" {
					msg = "not found"
				}
				errs[r] = fmt.Errorf("%s: %s", r, msg)
				continue
			}
			threads[r] = repo.PullRequest.pullRequest().OpenThreads
		}
	}
	return threads, errs, nil
}

// byRef reads in batches. A batch is one attempt; when GitHub fails it for
// time, byRef backs off once and reads each half in one attempt, and a half
// that fails again is reported in errs for each of its pull requests. A lone
// pull request gets the client's retries of a gateway status or a network
// failure.
func (c *Client) byRef(ctx context.Context, refs []Ref, size int, spread, fragment string, keep func(Ref, json.RawMessage) error) (rl dgh.RateLimit, errs map[Ref]error, err error) {
	errs = map[Ref]error{}
	read := func(chunk []Ref, attempts int) error {
		brl, berr := c.batch(ctx, chunk, spread, fragment, keep, errs, attempts)
		if berr != nil {
			return berr
		}
		cost := rl.Cost + brl.Cost
		rl = brl
		rl.Cost = cost
		return nil
	}
	lost := func(chunk []Ref, err error) {
		for _, r := range chunk {
			errs[r] = fmt.Errorf("%s: %w", r, err)
		}
	}
	if len(refs) == 0 {
		brl, berr := c.batch(ctx, nil, spread, fragment, keep, errs, c.retry.Attempts)
		return brl, errs, berr
	}
	for start := 0; start < len(refs); start += size {
		chunk := refs[start:min(start+size, len(refs))]
		attempts := 1
		if len(chunk) == 1 {
			attempts = c.retry.Attempts
		}
		berr := read(chunk, attempts)
		switch {
		case berr == nil:
			continue
		case !splittable(berr):
			return dgh.RateLimit{}, nil, berr
		case len(chunk) == 1:
			lost(chunk, berr)
			continue
		}
		if err := c.sleep(ctx, c.backoff(0)); err != nil {
			return dgh.RateLimit{}, nil, err
		}
		half := len(chunk) / 2
		for _, part := range [][]Ref{chunk[:half], chunk[half:]} {
			if herr := read(part, 1); herr != nil {
				if !splittable(herr) {
					return dgh.RateLimit{}, nil, herr
				}
				lost(part, herr)
			}
		}
	}
	return rl, errs, nil
}

func decodePR(keep func(Ref, *rawPR)) func(Ref, json.RawMessage) error {
	return func(r Ref, raw json.RawMessage) error {
		var p rawPR
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		keep(r, &p)
		return nil
	}
}

func splittable(err error) bool { return Transient(err) && !errors.Is(err, errBreaker) }

func (c *Client) batch(ctx context.Context, chunk []Ref, spread, fragment string, keep func(Ref, json.RawMessage) error, errs map[Ref]error, attempts int) (dgh.RateLimit, error) {
	var b strings.Builder
	b.WriteString("query {\n")
	b.WriteString("  " + rateLimitSelection + "\n")
	for i, r := range chunk {
		fmt.Fprintf(&b, "  pr%d: repository(owner: %s, name: %s) { pullRequest(number: %d) { ...%s } }\n",
			i, lit(r.Owner), lit(r.Repo), r.Number, spread)
	}
	b.WriteString("}\n")
	if len(chunk) > 0 {
		b.WriteString(fragment)
	}
	data, aliasErrs, err := c.graphQL(ctx, b.String(), nil, attempts)
	if err != nil {
		return dgh.RateLimit{}, err
	}
	for i, r := range chunk {
		alias := fmt.Sprintf("pr%d", i)
		var repo struct {
			PullRequest json.RawMessage `json:"pullRequest"`
		}
		if raw := data[alias]; len(raw) > 0 && string(raw) != "null" {
			if err := json.Unmarshal(raw, &repo); err != nil {
				errs[r] = err
				continue
			}
		}
		if len(repo.PullRequest) == 0 || string(repo.PullRequest) == "null" {
			msg := aliasErrs[alias]
			if msg == "" {
				msg = "not found"
			}
			errs[r] = fmt.Errorf("%s: %s", r, msg)
			continue
		}
		if err := keep(r, repo.PullRequest); err != nil {
			errs[r] = fmt.Errorf("%s: %w", r, err)
		}
	}
	return parseRateLimit(data["rateLimit"]), nil
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

// SearchStubs reads one page of a pull request search; after is the
// previous page's EndCursor, empty for the first page.
func (c *Client) SearchStubs(ctx context.Context, q, after string) (StubPage, error) {
	cursor := "null"
	if after != "" {
		cursor = lit(after)
	}
	query := fmt.Sprintf(`query {
  %s
  search(query: %s, type: ISSUE, first: %d, after: %s) { pageInfo { hasNextPage endCursor } nodes { ...stub } }
}
%s`, rateLimitSelection, lit(q), SearchFirst, cursor, stubFragment)
	data, aliasErrs, err := c.query(ctx, query)
	if err != nil {
		return StubPage{}, err
	}
	return parseStubPage(data["search"], aliasErrs["search"], parseRateLimit(data["rateLimit"]))
}

// DiscoveryFirst is the first page size of the fast discovery searches: a
// search pays its first times every nested connection, and a quiet window
// returns a handful of pull requests.
const DiscoveryFirst = 10

// SearchFirst is the page size of a listing expected to fill pages.
const SearchFirst = 100

// SearchStubsFirst reads the first page, of first stubs, of every query in
// one request; errs holds, per query, the error of that search alone.
func (c *Client) SearchStubsFirst(ctx context.Context, qs []string, first int) (pages []StubPage, errs []error, err error) {
	if len(qs) == 0 {
		return nil, nil, nil
	}
	var b strings.Builder
	b.WriteString("query {\n  " + rateLimitSelection + "\n")
	for i, q := range qs {
		fmt.Fprintf(&b, "  s%d: search(query: %s, type: ISSUE, first: %d) { pageInfo { hasNextPage endCursor } nodes { ...stub } }\n", i, lit(q), first)
	}
	b.WriteString("}\n" + stubFragment)
	data, aliasErrs, err := c.query(ctx, b.String())
	if err != nil {
		return nil, nil, err
	}
	rl := parseRateLimit(data["rateLimit"])
	pages, errs = make([]StubPage, len(qs)), make([]error, len(qs))
	for i := range qs {
		alias := fmt.Sprintf("s%d", i)
		pages[i], errs[i] = parseStubPage(data[alias], aliasErrs[alias], rl)
	}
	return pages, errs, nil
}

// StubBatch is the largest by-ref stub batch GitHub prices at one point.
const StubBatch = 40

// StubsByRef reads the stub of every ref in requests of StubBatch aliases.
// errs holds the refs GitHub did not return; err fails the whole read.
func (c *Client) StubsByRef(ctx context.Context, refs []Ref) (map[Ref]Stub, map[Ref]error, error) {
	stubs := map[Ref]Stub{}
	if len(refs) == 0 {
		return stubs, map[Ref]error{}, nil
	}
	_, errs, err := c.byRef(ctx, refs, StubBatch, "stub", stubFragment, func(r Ref, raw json.RawMessage) error {
		var n rawStub
		if err := json.Unmarshal(raw, &n); err != nil {
			return err
		}
		s := n.stub()
		s.Ref = r
		stubs[r] = s
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return stubs, errs, nil
}

type rawStub struct {
	Number         int       `json:"number"`
	State          string    `json:"state"`
	IsDraft        bool      `json:"isDraft"`
	UpdatedAt      time.Time `json:"updatedAt"`
	Mergeable      string    `json:"mergeable"`
	HeadRefOid     string    `json:"headRefOid"`
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

func (n rawStub) stub() Stub {
	owner, repo, _ := strings.Cut(n.Repository.NameWithOwner, "/")
	s := Stub{
		Ref: Ref{Owner: owner, Repo: repo, Number: n.Number}, Author: n.Author.name(), UpdatedAt: n.UpdatedAt,
		IsDraft: n.IsDraft, HeadOid: n.HeadRefOid, ReviewDecision: n.ReviewDecision,
		NameWithOwner: n.Repository.NameWithOwner, State: n.State, Mergeable: n.Mergeable,
	}
	for _, rr := range n.ReviewRequests.Nodes {
		if name := reviewerName(rr.RequestedReviewer); name != "" {
			s.ReviewRequests = append(s.ReviewRequests, name)
		}
	}
	if h := n.HeadCommit.Nodes; len(h) > 0 && h[0].Commit.StatusCheckRollup != nil {
		rollup := h[0].Commit.StatusCheckRollup
		s.ChecksState = rollupState(rollup.State)
		for _, n := range rollup.Contexts.Nodes {
			if c := n.check(); c.Failed {
				s.FailedChecks = append(s.FailedChecks, c.Name)
			}
		}
		slices.Sort(s.FailedChecks)
	}
	return s
}

func parseStubPage(raw json.RawMessage, aliasErr string, rl dgh.RateLimit) (StubPage, error) {
	if aliasErr != "" {
		return StubPage{}, fmt.Errorf("graphql search: %s", aliasErr)
	}
	if len(raw) == 0 || string(raw) == "null" {
		return StubPage{}, fmt.Errorf("graphql search: no result")
	}
	var res struct {
		PageInfo rawPageInfo `json:"pageInfo"`
		Nodes    []rawStub   `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return StubPage{}, fmt.Errorf("graphql search: %w", err)
	}
	if res.PageInfo.HasNextPage && res.PageInfo.EndCursor == "" {
		return StubPage{}, fmt.Errorf("graphql search: next page without a cursor")
	}
	page := StubPage{HasNext: res.PageInfo.HasNextPage, EndCursor: res.PageInfo.EndCursor, RateLimit: rl}
	for _, n := range res.Nodes {
		if n.Number == 0 {
			continue
		}
		page.Stubs = append(page.Stubs, n.stub())
	}
	return page, nil
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

type BranchQuery struct {
	Owner, Repo, Branch string
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

func (c *Client) FindByBranch(ctx context.Context, queries []BranchQuery) (map[BranchQuery][]BranchMatch, error) {
	out := map[BranchQuery][]BranchMatch{}
	if len(queries) == 0 {
		return out, nil
	}
	var b strings.Builder
	b.WriteString("query {\n  " + rateLimitSelection + "\n")
	for i, q := range queries {
		search := fmt.Sprintf("repo:%s/%s is:pr head:%s", q.Owner, q.Repo, q.Branch)
		fmt.Fprintf(&b, "  b%d: search(query: %s, type: ISSUE, first: 10) { nodes { ... on PullRequest { number state headRefName } } }\n", i, lit(search))
	}
	b.WriteString("}\n")
	data, _, err := c.query(ctx, b.String())
	if err != nil {
		return nil, err
	}
	for i, q := range queries {
		var res struct {
			Nodes []struct {
				Number      int    `json:"number"`
				State       string `json:"state"`
				HeadRefName string `json:"headRefName"`
			} `json:"nodes"`
		}
		if raw := data[fmt.Sprintf("b%d", i)]; len(raw) > 0 {
			_ = json.Unmarshal(raw, &res)
		}
		for _, n := range res.Nodes {
			if n.Number != 0 && n.HeadRefName == q.Branch {
				out[q] = append(out[q], BranchMatch{Number: n.Number, State: n.State})
			}
		}
	}
	return out, nil
}

func (c *Client) Basics(ctx context.Context, r Ref) (*PullRequest, error) {
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
