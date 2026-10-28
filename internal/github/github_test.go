package github_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	dgh "github.com/wloophq/wloop/internal/domain/github"
	"github.com/wloophq/wloop/internal/ghcli"
	. "github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/github/githubtest"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

func New(r ghcli.Runner) *Client { return githubtest.Runner(r) }

func lit(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestParseURL(t *testing.T) {
	cases := []struct {
		in   string
		want Ref
		err  bool
	}{
		{in: "https://github.com/acme/api/pull/7", want: Ref{Owner: "acme", Repo: "api", Number: 7}},
		{in: " https://github.com/acme/api/pull/7/files ", want: Ref{Owner: "acme", Repo: "api", Number: 7}},
		{in: "https://github.com/acme/api/pull/7#discussion_r1", want: Ref{Owner: "acme", Repo: "api", Number: 7}},
		{in: "https://github.com/acme/api/issues/7", err: true},
		{in: "https://gitlab.com/acme/api/pull/7", err: true},
		{in: "https://github.com/acme/api/pull/0", err: true},
		{in: "acme/api#7", err: true},
	}
	for _, c := range cases {
		got, err := ParseURL(c.in)
		if (err != nil) != c.err || (!c.err && got != c.want) {
			t.Errorf("ParseURL(%q) = %v, %v", c.in, got, err)
		}
	}
	if got := (Ref{Owner: "acme", Repo: "api", Number: 7}).URL(); got != "https://github.com/acme/api/pull/7" {
		t.Errorf("URL = %s", got)
	}
}

type runnerFunc func() ([]byte, error)

func (f runnerFunc) Run(context.Context, ...string) ([]byte, error) { return f() }

type queryRunner func(query string) ([]byte, error)

func (f queryRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	return f(args[len(args)-1])
}

func TestTeamMembersReportsGitHubError(t *testing.T) {
	c := New(runnerFunc(func() ([]byte, error) {
		return []byte(`{"data":{"organization":{"team":null}},"errors":[{"type":"INSUFFICIENT_SCOPES","path":["organization","team"],"message":"Your token has not been granted the required scopes"}]}`), nil
	}))
	_, err := c.TeamMembers(context.Background(), "acme", "search")
	if err == nil || !strings.Contains(err.Error(), "required scopes") {
		t.Fatalf("err %v", err)
	}

	c = New(runnerFunc(func() ([]byte, error) { return []byte(`{"data":{"organization":{"team":null}}}`), nil }))
	if _, err := c.TeamMembers(context.Background(), "acme", "search"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err %v", err)
	}
}

func TestTeamMembersPaginates(t *testing.T) {
	var queries []string
	c := New(queryRunner(func(q string) ([]byte, error) {
		queries = append(queries, q)
		if len(queries) == 1 {
			return []byte(`{"data":{"organization":{"team":{"members":{"pageInfo":{"hasNextPage":true,"endCursor":"c1"},"nodes":[{"login":"ana"}]}}}}}`), nil
		}
		return []byte(`{"data":{"organization":{"team":{"members":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[{"login":"dylan"}]}}}}}`), nil
	}))
	got, err := c.TeamMembers(context.Background(), "acme", "search")
	if err != nil || !slices.Equal(got, []string{"ana", "dylan"}) {
		t.Fatalf("%v %v", got, err)
	}
	if !strings.Contains(queries[0], "after: null") || !strings.Contains(queries[1], `after: "c1"`) {
		t.Fatalf("queries: %q", queries)
	}
}

func TestBasicsReadsTheAssociationAndYourReviews(t *testing.T) {
	gh := New(queryRunner(func(q string) ([]byte, error) {
		if !strings.Contains(q, "authorAssociation") || !strings.Contains(q, "viewerDidAuthor") {
			return []byte(`{"data":{"pr":{"pullRequest":{"number":8,"state":"OPEN"}}}}`), nil
		}
		return []byte(`{"data":{"pr":{"pullRequest":{"number":8,"state":"OPEN","authorAssociation":"FIRST_TIME_CONTRIBUTOR",
			"reviews":{"nodes":[{"state":"APPROVED","submittedAt":"2026-10-08T10:00:00Z","viewerDidAuthor":true,"commit":{"oid":"c1"}}]}}}}}`), nil
	}))
	pr, err := gh.Basics(context.Background(), Ref{Owner: "acme", Repo: "api", Number: 8})
	if err != nil || pr.AuthorAssociation != "FIRST_TIME_CONTRIBUTOR" || len(pr.Reviews) != 1 || !pr.Reviews[0].ByViewer || pr.Reviews[0].CommitOid != "c1" {
		t.Fatalf("%+v %v", pr, err)
	}
}

func TestBasicsReadsTheRepositoryVisibility(t *testing.T) {
	gh := New(queryRunner(func(q string) ([]byte, error) {
		if !strings.Contains(q, "repository { nameWithOwner visibility }") {
			return []byte(`{"data":{"pr":{"pullRequest":{"number":8,"state":"OPEN"}}}}`), nil
		}
		return []byte(`{"data":{"pr":{"pullRequest":{"number":8,"state":"OPEN","repository":{"nameWithOwner":"acme/api","visibility":"PUBLIC"}}}}}`), nil
	}))
	pr, err := gh.Basics(context.Background(), Ref{Owner: "acme", Repo: "api", Number: 8})
	if err != nil || pr.Visibility != "public" {
		t.Fatalf("visibility %q, err %v", pr.Visibility, err)
	}
}

func TestBasicsReadsWhetherYourReviewIsRequested(t *testing.T) {
	cases := map[string]struct {
		requested string
		want      bool
	}{
		"requested":         {`{"requestedReviewer":{"__typename":"User","login":"Me"}}`, true},
		"someone else":      {`{"requestedReviewer":{"__typename":"User","login":"bia"}}`, false},
		"a team you are in": {`{"requestedReviewer":{"__typename":"Team","combinedSlug":"acme/me"}}`, false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			gh := New(queryRunner(func(q string) ([]byte, error) {
				if !strings.Contains(q, "viewer { login }") || !strings.Contains(q, "reviewRequests(") {
					return []byte(`{"data":{"pr":{"pullRequest":{"number":8,"state":"OPEN"}}}}`), nil
				}
				return fmt.Appendf(nil, `{"data":{"viewer":{"login":"me"},"pr":{"pullRequest":{"number":8,"state":"OPEN","reviewRequests":{"nodes":[%s]}}}}}`, c.requested), nil
			}))
			pr, err := gh.Basics(context.Background(), Ref{Owner: "acme", Repo: "api", Number: 8})
			if err != nil || pr.ViewerRequested != c.want {
				t.Fatalf("requested %v, want %v, err %v (%v)", pr.ViewerRequested, c.want, err, pr.ReviewRequests)
			}
		})
	}
}

func TestBasicsReadsTheAuthor(t *testing.T) {
	cases := map[string]struct{ typename, login, want string }{
		"user": {"User", "me", "me"},
		"bot":  {"Bot", "dependabot", "dependabot[bot]"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			gh := New(queryRunner(func(q string) ([]byte, error) {
				if !strings.Contains(q, "author { __typename login }") {
					return []byte(`{"data":{"pr":{"pullRequest":{"number":8,"state":"OPEN"}}}}`), nil
				}
				return fmt.Appendf(nil, `{"data":{"pr":{"pullRequest":{"number":8,"state":"OPEN","author":{"__typename":%q,"login":%q}}}}}`, c.typename, c.login), nil
			}))
			pr, err := gh.Basics(context.Background(), Ref{Owner: "acme", Repo: "api", Number: 8})
			if err != nil || pr.Author != c.want {
				t.Fatalf("author %q, err %v", pr.Author, err)
			}
		})
	}
}

func TestPickByBranch(t *testing.T) {
	cases := []struct {
		name    string
		matches []BranchMatch
		want    int
	}{
		{"none", nil, 0},
		{"single", []BranchMatch{{Number: 3, State: "MERGED"}}, 3},
		{"one open among several", []BranchMatch{{Number: 3, State: "MERGED"}, {Number: 4, State: "OPEN"}}, 4},
		{"ambiguous", []BranchMatch{{Number: 3, State: "OPEN"}, {Number: 4, State: "OPEN"}}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got, _ := PickByBranch(c.matches); got != c.want {
				t.Fatalf("got %d, want %d", got, c.want)
			}
		})
	}
}

const candidatePage = `{"data":{"rateLimit":{"cost":1,"remaining":4990,"limit":5000,"resetAt":"2026-10-05T11:00:00Z"},
"search":{"issueCount":41,"nodes":[
{"number":7,"title":"Batch export","url":"https://github.com/acme/api/pull/7","state":"OPEN","isDraft":true,
 "createdAt":"2026-10-01T10:00:00Z","updatedAt":"2026-10-04T10:00:00Z","additions":120,"deletions":4,"changedFiles":3,
 "headRefName":"ana/batch","baseRefName":"main","reviewDecision":"REVIEW_REQUIRED",
 "repository":{"nameWithOwner":"acme/api"},"author":{"__typename":"User","login":"ana"},
 "reviewRequests":{"nodes":[{"requestedReviewer":{"__typename":"User","login":"bob"}},{"requestedReviewer":{"__typename":"Team","combinedSlug":"acme/search"}}]},
 "comments":{"totalCount":2},
 "mine":{"nodes":[{"state":"COMMENTED","commit":{"oid":"h1"}}]},
 "headCommit":{"nodes":[{"commit":{"oid":"h2","statusCheckRollup":{"state":"FAILURE"}}}]}},
{}
]}}}`

func TestSearchCandidatesParsesTheFacts(t *testing.T) {
	c := New(runnerFunc(func() ([]byte, error) { return []byte(candidatePage), nil }))
	page, err := c.SearchCandidates(context.Background(), "is:pr repo:acme/api", "me", 30)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 41 || page.RateLimit.Cost != 1 || len(page.Candidates) != 1 {
		t.Fatalf("page: %+v", page)
	}
	got := page.Candidates[0]
	want := dgh.Candidate{Ref: Ref{Owner: "acme", Repo: "api", Number: 7}, URL: "https://github.com/acme/api/pull/7", Title: "Batch export",
		Author: "ana", State: "OPEN", IsDraft: true, CreatedAt: time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC), Additions: 120, Deletions: 4, ChangedFiles: 3,
		HeadBranch: "ana/batch", BaseBranch: "main", ReviewDecision: "REVIEW_REQUIRED", ReviewRequests: []string{"bob", "team:acme/search"},
		ChecksState: "FAILURE", Comments: 2, MyReview: "COMMENTED", MyReviewOnHead: false}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidate:\n got %+v\nwant %+v", got, want)
	}
}

func TestSearchCandidatesQuotesTheQuery(t *testing.T) {
	var queries []string
	c := New(queryRunner(func(q string) ([]byte, error) {
		queries = append(queries, q)
		return []byte(candidatePage), nil
	}))
	q := `fix "login" \ in:title`
	c.SearchCandidates(context.Background(), q, "me", 30)
	c.SearchCandidates(context.Background(), q, "", 30)
	if !strings.Contains(queries[0], "search(query: "+lit(q)+", type: ISSUE, first: 30)") {
		t.Fatalf("query not quoted: %s", queries[0])
	}
	if !strings.Contains(queries[0], `reviews(last: 1, author: "me")`) || strings.Contains(queries[1], "mine:") {
		t.Fatalf("my review asked without a viewer, or not with one:\n%s\n%s", queries[0], queries[1])
	}
}

func TestCandidateByRef(t *testing.T) {
	node := candidatePage[strings.Index(candidatePage, `{"number":7`):strings.Index(candidatePage, ",\n{}")]
	ok := `{"data":{"rateLimit":{"cost":1,"remaining":4990,"limit":5000,"resetAt":"2026-10-05T11:00:00Z"},"pr":{"pullRequest":` + node + `}}}`
	c := New(runnerFunc(func() ([]byte, error) { return []byte(ok), nil }))
	page, err := c.CandidateByRef(context.Background(), Ref{Owner: "acme", Repo: "api", Number: 7}, "me")
	if err != nil || page.Total != 1 || len(page.Candidates) != 1 || page.Candidates[0].Title != "Batch export" {
		t.Fatalf("%+v %v", page, err)
	}
	missing := `{"data":{"pr":null},"errors":[{"path":["pr"],"message":"Could not resolve to a Repository with the name 'acme/nope'."}]}`
	c = New(runnerFunc(func() ([]byte, error) { return []byte(missing), nil }))
	if _, err := c.CandidateByRef(context.Background(), Ref{Owner: "acme", Repo: "nope", Number: 1}, "me"); err == nil || !strings.Contains(err.Error(), "Could not resolve") {
		t.Fatalf("missing repository: %v", err)
	}
}

type argsRunner func(args []string) ([]byte, error)

func (f argsRunner) Run(_ context.Context, args ...string) ([]byte, error) { return f(args) }

func TestIsUser(t *testing.T) {
	var asked [][]string
	c := New(argsRunner(func(args []string) ([]byte, error) {
		asked = append(asked, args)
		switch args[1] {
		case "users/judy":
			return []byte(`{"login":"judy","type":"User"}`), nil
		case "users/acme":
			return []byte(`{"login":"acme","type":"Organization"}`), nil
		case "users/invoices":
			return []byte(`{"message":"Not Found","status":"404"}`), fmt.Errorf("gh api users/invoices: gh: Not Found (HTTP 404)")
		}
		return nil, fmt.Errorf("gh api %s: HTTP 502", args[1])
	}))
	for login, want := range map[string]bool{"judy": true, "acme": false, "invoices": false} {
		if got, err := c.IsUser(context.Background(), login); err != nil || got != want {
			t.Errorf("IsUser(%q) = %v, %v; want %v", login, got, err, want)
		}
	}
	if _, err := c.IsUser(context.Background(), "flaky"); err == nil {
		t.Error("a gateway error read as an answer")
	}
	if !slices.Equal(asked[0], []string{"api", asked[0][1]}) {
		t.Errorf("asked %v", asked[0])
	}
	n := len(asked)
	if _, err := c.IsUser(context.Background(), "../rate_limit"); err == nil || len(asked) != n {
		t.Errorf("a word that is not a login reached GitHub: %v %v", err, asked)
	}
}

func TestSearchCandidatesLeavesTheRetryToTheCaller(t *testing.T) {
	calls := 0
	c := New(runnerFunc(func() ([]byte, error) { calls++; return nil, fmt.Errorf("gh api graphql: HTTP 502") }))
	if _, err := c.SearchCandidates(context.Background(), "is:pr", "me", 30); err == nil || !Transient(err) || calls != 1 {
		t.Fatalf("%v after %d calls", err, calls)
	}
}

func TestRemoteKey(t *testing.T) {
	if got := (Ref{Owner: "acme", Repo: "api", Number: 7}).RemoteKey(); got != "github.com/acme/api" {
		t.Fatalf("remote key %q", got)
	}
}

func TestBasicsReadLabels(t *testing.T) {
	gh := New(queryRunner(func(q string) ([]byte, error) {
		if !strings.Contains(q, "labels(first: 50) { nodes { name } }") {
			return []byte(`{"data":{"pr":{"pullRequest":{"number":8,"state":"OPEN"}}}}`), nil
		}
		return []byte(`{"data":{"pr":{"pullRequest":{"number":8,"state":"OPEN","labels":{"nodes":[{"name":"bug"},{"name":"area/api"}]}}}}}`), nil
	}))
	pr, err := gh.Basics(context.Background(), Ref{Owner: "acme", Repo: "api", Number: 8})
	if err != nil || !slices.Equal(pr.Labels, []string{"bug", "area/api"}) {
		t.Fatalf("labels %v, err %v", pr.Labels, err)
	}
}

func TestRetryCountsTwoCalls(t *testing.T) {
	ctx, s := metertest.Start(t, context.Background())
	calls := 0
	gh := New(runnerFunc(func() ([]byte, error) {
		calls++
		if calls == 1 {
			return nil, fmt.Errorf("gh api graphql -f: HTTP 502")
		}
		return []byte(`{"data":{"viewer":{"login":"me"}}}`), nil
	}))
	if _, err := gh.Viewer(ctx); err != nil {
		t.Fatal(err)
	}
	for l, want := range map[metertest.Limit]int64{
		{Res: meter.GitHub, Op: "graphql"}:                          2,
		{Res: meter.GitHub, Op: "graphql", Field: metertest.Errors}: 1,
	} {
		if got := metertest.Count(s, l); got != want {
			t.Errorf("%s = %d, want %d", l, got, want)
		}
	}
}

func TestEveryQuerySelectsRateLimit(t *testing.T) {
	var queries []string
	gh := New(queryRunner(func(q string) ([]byte, error) {
		queries = append(queries, q)
		return []byte(`{"data":{"viewer":{"login":"me"}}}`), nil
	}))
	ctx := context.Background()
	ref := Ref{Owner: "acme", Repo: "api", Number: 1}
	_, _ = gh.Viewer(ctx)
	_, _ = gh.SearchCandidates(ctx, "is:pr", "me", 10)
	_, _ = gh.SearchCandidates(ctx, "is:pr", "", 10)
	_, _ = gh.CandidateByRef(ctx, ref, "me")
	_, _ = gh.TeamMembers(ctx, "acme", "core")
	_, _ = gh.Basics(ctx, ref)
	if len(queries) != 6 {
		t.Fatalf("%d documents sent, want 6", len(queries))
	}
	for _, q := range queries {
		if !strings.Contains(q, "rateLimit {") {
			t.Errorf("document without rateLimit:\n%s", q)
		}
	}
}
