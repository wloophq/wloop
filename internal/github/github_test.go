package github_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	dgh "github.com/wloophq/wloop/internal/domain/github"
	"github.com/wloophq/wloop/internal/domain/pr"
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

type gatewayRunner struct {
	max       int
	poison    int
	queries   int
	succeeded int
	sawViewer bool
}

var aliasRe = regexp.MustCompile(`(pr\d+): repository\(owner: "[^"]+", name: "[^"]+"\) \{ pullRequest\(number: (\d+)\)`)

func (g *gatewayRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	g.queries++
	q := args[len(args)-1]
	if strings.Contains(q, "\n  viewer {") {
		g.sawViewer = true
	}
	aliases := aliasRe.FindAllStringSubmatch(q, -1)
	if len(aliases) > g.max {
		return nil, fmt.Errorf("gh api graphql -f: HTTP 502")
	}
	data := map[string]any{
		"viewer":    map[string]string{"login": "me"},
		"rateLimit": map[string]any{"cost": 1, "remaining": 5000 - g.queries, "limit": 5000, "resetAt": "2026-10-05T11:00:00Z"},
	}
	for _, a := range aliases {
		n, _ := strconv.Atoi(a[2])
		if n == g.poison {
			return nil, fmt.Errorf("gh api graphql -f: HTTP 504")
		}
		data[a[1]] = map[string]any{"pullRequest": map[string]any{"number": n, "repository": map[string]string{"nameWithOwner": "acme/api"}}}
	}
	g.succeeded++
	return json.Marshal(map[string]any{"data": data})
}

func TestByRefSplitsABatchGitHubTimesOutOnOnce(t *testing.T) {
	var refs []Ref
	for n := 1; n <= 25; n++ {
		refs = append(refs, Ref{Owner: "acme", Repo: "api", Number: n})
	}
	g := &gatewayRunner{max: 10, poison: 13}
	prs, errs, _, err := New(g).InventoryByRef(context.Background(), refs)
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 15 || len(errs) != 10 || errs[Ref{Owner: "acme", Repo: "api", Number: 13}] == nil || errs[Ref{Owner: "acme", Repo: "api", Number: 20}] == nil {
		t.Fatalf("read %d, errs %v", len(prs), errs)
	}
	if g.queries != 4 {
		t.Fatalf("%d queries, want 4: the batch of 20, its two halves, the batch of 5", g.queries)
	}
}

func TestByRefFailsOnANonTransientError(t *testing.T) {
	r := runnerFunc(func() ([]byte, error) { return nil, fmt.Errorf("gh api graphql -f: HTTP 401") })
	if _, _, _, err := New(r).InventoryByRef(context.Background(), []Ref{{Owner: "acme", Repo: "api", Number: 1}, {Owner: "acme", Repo: "api", Number: 2}}); err == nil {
		t.Fatal("want error")
	}
}

type runnerFunc func() ([]byte, error)

func (f runnerFunc) Run(context.Context, ...string) ([]byte, error) { return f() }

type queryRunner func(query string) ([]byte, error)

func (f queryRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	return f(args[len(args)-1])
}

func TestInventoryByRefSumsCostAcrossBatches(t *testing.T) {
	var refs []Ref
	for n := 1; n <= 25; n++ {
		refs = append(refs, Ref{Owner: "acme", Repo: "api", Number: n})
	}
	g := &gatewayRunner{max: 10}
	snaps, errs, rl, err := New(g).InventoryByRef(context.Background(), refs)
	if err != nil || len(snaps) != 25 || len(errs) != 0 {
		t.Fatalf("read %d, errs %v, err %v", len(snaps), errs, err)
	}
	if g.succeeded < 2 || rl.Cost != g.succeeded || rl.Remaining != 5000-g.queries || rl.Limit != 5000 {
		t.Fatalf("%d batches, %d queries, rate limit %+v", g.succeeded, g.queries, rl)
	}
	if g.sawViewer {
		t.Fatal("inventory query asked for the viewer")
	}

	empty := &gatewayRunner{max: 6}
	if snaps, _, _, err := New(empty).InventoryByRef(context.Background(), nil); err != nil || len(snaps) != 0 || empty.queries != 0 {
		t.Fatalf("no refs: %d snapshots, %d queries, err %v", len(snaps), empty.queries, err)
	}
}

const stubPage = `{"data":{"rateLimit":{"cost":1,"remaining":4990,"limit":5000,"resetAt":"2026-10-05T11:00:00Z"},
"search":{"pageInfo":{"hasNextPage":true,"endCursor":"Y3Vyc29y"},"nodes":[{
 "number":7,"state":"MERGED","updatedAt":"2026-10-05T09:00:00Z","mergeable":"UNKNOWN","repository":{"nameWithOwner":"acme/api"},"author":{"login":"ana"},
 "reviewRequests":{"nodes":[{"requestedReviewer":{"login":"me"}},{"requestedReviewer":{"combinedSlug":"acme/search"}}]},
 "headCommit":{"nodes":[{"commit":{"statusCheckRollup":{"state":"FAILURE","contexts":{"nodes":[
  {"name":"lint","status":"COMPLETED","conclusion":"FAILURE"},
  {"name":"build","status":"COMPLETED","conclusion":"SUCCESS"},
  {"context":"Preview site","state":"ERROR"},
  {}
 ]}}}}]}
}]}}}`

const inventoryByRef = `{"data":{"rateLimit":{"cost":12,"remaining":4978,"limit":5000,"resetAt":"2026-10-05T11:00:00Z"},
"pr0":{"pullRequest":{
 "number":7,"title":"Batch export","state":"OPEN","isDraft":false,"url":"https://github.com/acme/api/pull/7",
 "createdAt":"2026-10-01T10:00:00Z","updatedAt":"2026-10-05T09:00:00Z","mergedAt":null,"closedAt":null,
 "lastEditedAt":"2026-10-05T08:00:00Z","changedFiles":4,
 "headRefName":"feat/batch","baseRefName":"main","headRefOid":"h2","isCrossRepository":false,
 "repository":{"nameWithOwner":"acme/api","visibility":"PRIVATE"},"author":{"login":"ana"},"authorAssociation":"MEMBER","reviewDecision":"REVIEW_REQUIRED","mergeable":"CONFLICTING",
 "headRepositoryOwner":{"login":"acme"},
 "commits":{"nodes":[{"commit":{"oid":"h1","committedDate":"2026-10-01T10:00:00Z","parents":{"totalCount":1}}},{"commit":{"oid":"h2","committedDate":"2026-10-02T10:00:00Z","parents":{"totalCount":1}}}]},
 "reviews":{"nodes":[{"id":"r1","state":"COMMENTED","submittedAt":"2026-10-03T10:00:00Z","author":{"login":"me"},"commit":{"oid":"h2"}}]},
 "comments":{"nodes":[{"id":"c1","createdAt":"2026-10-04T10:00:00Z","author":{"login":"ana"}}]},
 "reviewRequests":{"nodes":[{"requestedReviewer":{"login":"me"}},{"requestedReviewer":{"combinedSlug":"acme/search"}}]},
 "timelineItems":{"nodes":[{"__typename":"ReviewRequestedEvent","createdAt":"2026-10-02T11:00:00Z","requestedReviewer":{"login":"me"}},{"__typename":"ReadyForReviewEvent","createdAt":"2026-10-02T10:30:00Z"}]},
 "headCommit":{"nodes":[{"commit":{"oid":"h2","statusCheckRollup":{"state":"FAILURE","contexts":{"nodes":[
  {"__typename":"CheckRun","databaseId":114268776435,"name":"Build and test","status":"COMPLETED","conclusion":"FAILURE","title":null,"detailsUrl":"https://github.com/acme/api/actions/runs/38071193224/job/114268776435"},
  {"__typename":"StatusContext","context":"Preview site","state":"FAILURE","description":"Build ou deploy do preview falhou","targetUrl":null},
  {"__typename":"CheckRun","name":"lint","status":"COMPLETED","conclusion":"SUCCESS","title":null,"detailsUrl":"https://github.com/acme/api/actions/runs/38071193224/job/114268776436"}
 ]}}}}]}
}}}}`

func TestSearchStubsParsesPage(t *testing.T) {
	c := New(runnerFunc(func() ([]byte, error) { return []byte(stubPage), nil }))
	page, err := c.SearchStubs(context.Background(), "is:pr is:open author:ana", "")
	if err != nil {
		t.Fatal(err)
	}
	if !page.HasNext || page.EndCursor != "Y3Vyc29y" || page.RateLimit.Remaining != 4990 || page.RateLimit.ResetAt.IsZero() {
		t.Fatalf("page: %+v", page)
	}
	if len(page.Stubs) != 1 {
		t.Fatalf("stubs: %d", len(page.Stubs))
	}
	s := page.Stubs[0]
	if s.Ref != (Ref{Owner: "acme", Repo: "api", Number: 7}) || s.Author != "ana" || s.UpdatedAt.IsZero() || s.NameWithOwner != "acme/api" || s.State != "MERGED" {
		t.Fatalf("%+v", s)
	}
	if !slices.Equal(s.ReviewRequests, []string{"me", "team:acme/search"}) {
		t.Fatalf("requests: %v", s.ReviewRequests)
	}
	if s.Mergeable != "UNKNOWN" {
		t.Fatalf("mergeable %q", s.Mergeable)
	}
	if s.ChecksState != "FAILURE" || !slices.Equal(s.FailedChecks, []string{"Preview site", "lint"}) {
		t.Fatalf("checks: %s %v", s.ChecksState, s.FailedChecks)
	}
}

func TestInventoryByRefParsesSnapshot(t *testing.T) {
	c := New(runnerFunc(func() ([]byte, error) { return []byte(inventoryByRef), nil }))
	ref := Ref{Owner: "acme", Repo: "api", Number: 7}
	snaps, errs, rl, err := c.InventoryByRef(context.Background(), []Ref{ref})
	if err != nil || len(errs) != 0 || rl.Cost != 12 {
		t.Fatalf("%v %v %+v", err, errs, rl)
	}
	s, ok := snaps[ref]
	if !ok {
		t.Fatalf("missing snapshot: %v", snaps)
	}
	if s.Mergeable != "CONFLICTING" {
		t.Fatalf("mergeable %q", s.Mergeable)
	}
	if s.Author != "ana" || s.HeadRefOid != "h2" || s.ChecksState != "FAILURE" || s.URL == "" {
		t.Fatalf("%+v", s)
	}
	if s.Visibility != "private" {
		t.Fatalf("visibility %q", s.Visibility)
	}
	var failed []pr.Check
	for _, c := range s.Checks {
		if c.Failed {
			failed = append(failed, c)
		}
	}
	want := []pr.Check{
		{Name: "Build and test", Failed: true, URL: "https://github.com/acme/api/actions/runs/38071193224/job/114268776435"},
		{Name: "Preview site", Failed: true, Summary: "Build ou deploy do preview falhou"},
	}
	if !slices.Equal(failed, want) {
		t.Fatalf("failed checks %+v", failed)
	}
	if len(s.CommitsDated) != 2 || s.CommitsDated[1].At.IsZero() {
		t.Fatalf("commits: %+v", s.CommitsDated)
	}
	if len(s.ReviewRequests) != 1 || s.ReviewRequests[0].Reviewer != "me" {
		t.Fatalf("requests: %+v", s.ReviewRequests)
	}
	if !slices.Equal(s.PullRequest.ReviewRequests, []string{"me", "team:acme/search"}) {
		t.Fatalf("pending: %v", s.PullRequest.ReviewRequests)
	}
	if s.ReadyAt.IsZero() || s.UpdatedAt.IsZero() {
		t.Fatalf("times: %+v", s)
	}
	if s.ChangedFiles != 4 || !s.LastEditedAt.Equal(time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("edited %v, files %d", s.LastEditedAt, s.ChangedFiles)
	}
}

func TestInventoryByRefReadsANeverEditedDescriptionAsZero(t *testing.T) {
	raw := strings.Replace(inventoryByRef, `"lastEditedAt":"2026-10-05T08:00:00Z"`, `"lastEditedAt":null`, 1)
	c := New(runnerFunc(func() ([]byte, error) { return []byte(raw), nil }))
	ref := Ref{Owner: "acme", Repo: "api", Number: 7}
	snaps, _, _, err := c.InventoryByRef(context.Background(), []Ref{ref})
	if err != nil || !snaps[ref].LastEditedAt.IsZero() || snaps[ref].ChangedFiles != 4 {
		t.Fatalf("%+v %v", snaps[ref], err)
	}
}

func TestSearchStubsForwardsCursor(t *testing.T) {
	var queries []string
	c := New(queryRunner(func(q string) ([]byte, error) {
		queries = append(queries, q)
		return []byte(stubPage), nil
	}))
	for _, after := range []string{"", "Y3Vyc29y"} {
		if _, err := c.SearchStubs(context.Background(), "is:pr", after); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(queries[0], "after: null") || !strings.Contains(queries[1], `after: "Y3Vyc29y"`) {
		t.Fatalf("queries: %q", queries)
	}
}

func TestSearchStubsFailsOnASearchError(t *testing.T) {
	cases := map[string]string{
		"invalid cursor": `{"data":{"rateLimit":{"cost":1,"remaining":4990,"limit":5000,"resetAt":"2026-10-05T11:00:00Z"},
"search":{"pageInfo":{"hasNextPage":true,"endCursor":null},"nodes":[]}},
"errors":[{"type":"INVALID_CURSOR_ARGUMENTS","path":["search","pageInfo","endCursor"],"message":"bad cursor"}]}`,
		"null search":                `{"data":{"search":null},"errors":[{"path":["search"],"message":"Something went wrong"}]}`,
		"next page without a cursor": `{"data":{"search":{"pageInfo":{"hasNextPage":true,"endCursor":null},"nodes":[]}}}`,
	}
	for name, body := range cases {
		c := New(runnerFunc(func() ([]byte, error) { return []byte(body), nil }))
		if page, err := c.SearchStubs(context.Background(), "is:pr", ""); err == nil {
			t.Errorf("%s: want error, got %+v", name, page)
		}
	}
}

func TestSearchStubsFirstSplitsTheBatchByAlias(t *testing.T) {
	body := strings.Replace(stubPage, `"search":`, `"s0":`, 1)
	body = strings.TrimSuffix(body, "}") + `,"errors":[{"path":["s1"],"message":"Something went wrong"}]}`
	c := New(runnerFunc(func() ([]byte, error) { return []byte(body), nil }))
	pages, errs, err := c.SearchStubsFirst(context.Background(), []string{"is:pr author:ana", "is:pr reviewed-by:me"}, SearchFirst)
	if err != nil {
		t.Fatal(err)
	}
	if errs[0] != nil || len(pages[0].Stubs) != 1 || pages[0].EndCursor != "Y3Vyc29y" || pages[0].RateLimit.Remaining != 4990 {
		t.Fatalf("first: %+v %v", pages[0], errs[0])
	}
	if errs[1] == nil {
		t.Fatalf("second: want its own error, got %+v", pages[1])
	}
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

const botStubPage = `{"data":{"search":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[{
 "number":8,"updatedAt":"2026-10-05T09:00:00Z","repository":{"nameWithOwner":"acme/api"},"author":{"__typename":"Bot","login":"dependabot"},
 "reviewRequests":{"nodes":[{"requestedReviewer":{"__typename":"Bot","login":"copilot-pull-request-reviewer"}},{"requestedReviewer":{"__typename":"User","login":"me"}}]}
}]}}}`

func TestSearchStubsSuffixesBots(t *testing.T) {
	c := New(runnerFunc(func() ([]byte, error) { return []byte(botStubPage), nil }))
	page, err := c.SearchStubs(context.Background(), "is:pr", "")
	if err != nil || len(page.Stubs) != 1 {
		t.Fatalf("%+v %v", page, err)
	}
	s := page.Stubs[0]
	if s.Author != "dependabot[bot]" || !slices.Equal(s.ReviewRequests, []string{"copilot-pull-request-reviewer[bot]", "me"}) {
		t.Fatalf("%+v", s)
	}
}

const botInventory = `{"data":{"rateLimit":{"cost":1,"remaining":4978,"limit":5000,"resetAt":"2026-10-05T11:00:00Z"},
"pr0":{"pullRequest":{
 "number":8,"state":"OPEN","url":"https://github.com/acme/api/pull/8","repository":{"nameWithOwner":"acme/api"},
 "author":{"__typename":"Bot","login":"dependabot"},
 "reviews":{"nodes":[
  {"id":"r1","state":"APPROVED","submittedAt":"2026-10-03T10:00:00Z","author":{"__typename":"User","login":"me"},"commit":{"oid":"h2"}},
  {"id":"r2","state":"COMMENTED","submittedAt":"2026-10-03T11:00:00Z","author":{"__typename":"Bot","login":"copilot-pull-request-reviewer"},"commit":{"oid":"h2"}},
  {"id":"r3","state":"PENDING","submittedAt":null,"author":{"__typename":"User","login":"me"},"commit":{"oid":"h2"}}]},
 "comments":{"nodes":[{"id":"c1","createdAt":"2026-10-04T10:00:00Z","author":{"__typename":"Bot","login":"codecov"}}]},
 "reviewRequests":{"nodes":[{"requestedReviewer":{"__typename":"Bot","login":"copilot-pull-request-reviewer"}}]},
 "timelineItems":{"nodes":[{"__typename":"ReviewRequestedEvent","createdAt":"2026-10-02T11:00:00Z","requestedReviewer":{"__typename":"Bot","login":"copilot-pull-request-reviewer"}}]},
 "headCommit":{"nodes":[{"commit":{"oid":"h2","statusCheckRollup":{"state":"%s","contexts":{"nodes":[]}}}}]}
}}}}`

func TestInventorySnapshotSuffixesBotsAndDropsPendingReviews(t *testing.T) {
	ref := Ref{Owner: "acme", Repo: "api", Number: 8}
	c := New(runnerFunc(func() ([]byte, error) { return []byte(strings.Replace(botInventory, "%s", "SUCCESS", 1)), nil }))
	snaps, errs, _, err := c.InventoryByRef(context.Background(), []Ref{ref})
	if err != nil || len(errs) != 0 {
		t.Fatalf("%v %v", err, errs)
	}
	s := snaps[ref]
	if s.Author != "dependabot[bot]" {
		t.Fatalf("author %q", s.Author)
	}
	var reviews []string
	for _, r := range s.Reviews {
		reviews = append(reviews, r.ID+":"+r.Author)
	}
	if !slices.Equal(reviews, []string{"r1:me", "r2:copilot-pull-request-reviewer[bot]"}) {
		t.Fatalf("reviews %v", reviews)
	}
	if len(s.Comments) != 1 || s.Comments[0].Author != "codecov[bot]" {
		t.Fatalf("comments %+v", s.Comments)
	}
	if !slices.Equal(s.PullRequest.ReviewRequests, []string{"copilot-pull-request-reviewer[bot]"}) {
		t.Fatalf("pending requests %v", s.PullRequest.ReviewRequests)
	}
	if len(s.ReviewRequests) != 1 || s.ReviewRequests[0].Reviewer != "copilot-pull-request-reviewer[bot]" {
		t.Fatalf("request events %+v", s.ReviewRequests)
	}
}

func TestInventorySnapshotNormalizesChecksState(t *testing.T) {
	ref := Ref{Owner: "acme", Repo: "api", Number: 8}
	for in, want := range map[string]string{"SUCCESS": "SUCCESS", "FAILURE": "FAILURE", "PENDING": "PENDING", "ERROR": "FAILURE", "EXPECTED": "PENDING"} {
		c := New(runnerFunc(func() ([]byte, error) { return []byte(strings.Replace(botInventory, "%s", in, 1)), nil }))
		snaps, _, _, err := c.InventoryByRef(context.Background(), []Ref{ref})
		if err != nil || snaps[ref].ChecksState != want {
			t.Errorf("%s: got %q, err %v, want %s", in, snaps[ref].ChecksState, err, want)
		}
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

const stubPageWithChecks = `{"data":{"search":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[
 {"number":7,"updatedAt":"2026-10-05T09:00:00Z","repository":{"nameWithOwner":"acme/api"},"author":{"login":"ana"},
  "headCommit":{"nodes":[{"commit":{"statusCheckRollup":{"state":"ERROR"}}}]}},
 {"number":8,"updatedAt":"2026-10-05T09:00:00Z","repository":{"nameWithOwner":"acme/api"},"author":{"login":"ana"},
  "headCommit":{"nodes":[{"commit":{"statusCheckRollup":null}}]}}
]}}}`

func TestSearchStubsReadsTheHeadChecksState(t *testing.T) {
	var query string
	c := New(queryRunner(func(q string) ([]byte, error) {
		query = q
		return []byte(stubPageWithChecks), nil
	}))
	page, err := c.SearchStubs(context.Background(), "is:pr", "")
	if err != nil || len(page.Stubs) != 2 {
		t.Fatalf("%+v %v", page, err)
	}
	if !strings.Contains(query, "number state isDraft updatedAt mergeable headRefOid reviewDecision") {
		t.Fatalf("the stub query does not read the version fields: %s", query)
	}
	if !strings.Contains(query, "statusCheckRollup { state contexts(first: 50)") {
		t.Fatalf("the stub query does not read the head checks: %s", query)
	}
	if page.Stubs[0].ChecksState != "FAILURE" || page.Stubs[1].ChecksState != "" {
		t.Fatalf("checks: %q %q", page.Stubs[0].ChecksState, page.Stubs[1].ChecksState)
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

func TestFindByBranchKeepsOnlyTheExactBranch(t *testing.T) {
	r := queryRunner(func(string) ([]byte, error) {
		return []byte(`{"data":{"b0":{"nodes":[{"number":7,"state":"OPEN","headRefName":"fix/ack"},{"number":8,"state":"OPEN","headRefName":"fix/ack-2"}]}}}`), nil
	})
	q := BranchQuery{Owner: "acme", Repo: "api", Branch: "fix/ack"}
	got, err := New(r).FindByBranch(context.Background(), []BranchQuery{q})
	if err != nil || len(got[q]) != 1 || got[q][0].Number != 7 {
		t.Fatalf("%+v %v", got, err)
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

func TestFullReadReadsTheMergeableState(t *testing.T) {
	c := New(runnerFunc(func() ([]byte, error) {
		return []byte(`{"data":{"viewer":{"login":"me"},"pr0":{"pullRequest":{"number":9,"state":"OPEN","headRefOid":"h1","reviewDecision":"APPROVED",
 "mergeable":"CONFLICTING","baseRefName":"main","repository":{"nameWithOwner":"acme/api"},"author":{"login":"me"}}}}}`), nil
	}))
	ref := Ref{Owner: "acme", Repo: "api", Number: 9}
	snaps, errs, _, err := c.InventoryByRef(context.Background(), []Ref{ref})
	if s, ok := snaps[ref]; err != nil || len(errs) != 0 || !ok || s.Mergeable != "CONFLICTING" || s.BaseRefName != "main" {
		t.Fatalf("%+v %v %v", snaps[ref], errs, err)
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

func TestEveryPullRequestReadNamesABotAuthorTheSameWay(t *testing.T) {
	const pr = `{"number":8,"state":"OPEN","repository":{"nameWithOwner":"acme/api"},"author":{%s"login":"github-actions"}}`
	gh := New(queryRunner(func(q string) ([]byte, error) {
		typename := ""
		if strings.Contains(q, "author { __typename login }") {
			typename = `"__typename":"Bot",`
		}
		node := fmt.Sprintf(pr, typename)
		return fmt.Appendf(nil, `{"data":{"viewer":{"login":"me"},"pr":{"pullRequest":%s},"pr0":{"pullRequest":%s},"requested":{"nodes":[%s]}}}`, node, node, node), nil
	}))
	ctx, ref := context.Background(), Ref{Owner: "acme", Repo: "api", Number: 8}
	authors := map[string]string{}
	if p, err := gh.Basics(ctx, ref); err == nil {
		authors["Basics"] = p.Author
	}
	if snaps, _, _, err := gh.InventoryByRef(ctx, []Ref{ref}); err == nil {
		authors["InventoryByRef"] = snaps[ref].Author
	}
	for _, read := range []string{"Basics", "InventoryByRef"} {
		if authors[read] != "github-actions[bot]" {
			t.Errorf("%s reads the author as %q", read, authors[read])
		}
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

func TestInventorySnapshotReadsTheMergerAndCommit(t *testing.T) {
	raw := strings.Replace(inventoryByRef, `"mergedAt":null,`, `"mergedAt":"2026-10-05T09:00:00Z","mergedBy":{"__typename":"Bot","login":"mergify"},"mergeCommit":{"oid":"m9"},`, 1)
	c := New(runnerFunc(func() ([]byte, error) { return []byte(raw), nil }))
	ref := Ref{Owner: "acme", Repo: "api", Number: 7}
	snaps, _, _, err := c.InventoryByRef(context.Background(), []Ref{ref})
	if s := snaps[ref]; err != nil || s.MergedBy != "mergify[bot]" || s.MergeCommit != "m9" {
		t.Fatalf("merged by %q, commit %q, err %v", s.MergedBy, s.MergeCommit, err)
	}
}

func TestInventorySnapshotReadsDraftsAndRemovedRequests(t *testing.T) {
	raw := strings.Replace(inventoryByRef, `{"__typename":"ReadyForReviewEvent","createdAt":"2026-10-02T10:30:00Z"}`,
		`{"__typename":"ConvertToDraftEvent","createdAt":"2026-10-02T10:10:00Z","actor":{"__typename":"User","login":"ana"}},`+
			`{"__typename":"ReadyForReviewEvent","createdAt":"2026-10-02T10:30:00Z"},`+
			`{"__typename":"ReviewRequestRemovedEvent","createdAt":"2026-10-02T12:00:00Z","actor":{"__typename":"User","login":"ana"},"requestedReviewer":{"__typename":"Team","combinedSlug":"acme/search"}}`, 1)
	raw = strings.Replace(raw, `"requestedReviewer":{"login":"me"}},{"__typename":"ConvertToDraftEvent"`, `"actor":{"__typename":"User","login":"bia"},"requestedReviewer":{"login":"me"}},{"__typename":"ConvertToDraftEvent"`, 1)
	c := New(runnerFunc(func() ([]byte, error) { return []byte(raw), nil }))
	ref := Ref{Owner: "acme", Repo: "api", Number: 7}
	snaps, _, _, err := c.InventoryByRef(context.Background(), []Ref{ref})
	if err != nil {
		t.Fatal(err)
	}
	s := snaps[ref]
	if len(s.Drafts) != 1 || s.Drafts[0].Actor != "ana" || !s.Drafts[0].At.Equal(time.Date(2026, 10, 2, 10, 10, 0, 0, time.UTC)) {
		t.Fatalf("drafts: %+v", s.Drafts)
	}
	if len(s.RemovedReviews) != 1 || s.RemovedReviews[0].Reviewer != "team:acme/search" || s.RemovedReviews[0].Actor != "ana" {
		t.Fatalf("removed: %+v", s.RemovedReviews)
	}
	if len(s.ReviewRequests) != 1 || s.ReviewRequests[0].Actor != "bia" || s.ReadyAt.IsZero() {
		t.Fatalf("requests %+v, ready %v", s.ReviewRequests, s.ReadyAt)
	}
}

func TestInventorySnapshotCountsWhatTheTimelineLeftOut(t *testing.T) {
	raw := strings.Replace(inventoryByRef, `"timelineItems":{"nodes"`, `"timelineItems":{"filteredCount":104,"nodes"`, 1)
	var query string
	c := New(queryRunner(func(q string) ([]byte, error) { query = q; return []byte(raw), nil }))
	ref := Ref{Owner: "acme", Repo: "api", Number: 7}
	snaps, _, _, err := c.InventoryByRef(context.Background(), []Ref{ref})
	if err != nil || snaps[ref].TimelineMissed != 102 || !strings.Contains(query, "timelineItems(last: 100,") {
		t.Fatalf("missed %d, err %v", snaps[ref].TimelineMissed, err)
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
	one, two := []Ref{ref}, []Ref{ref, {Owner: "acme", Repo: "api", Number: 2}}
	_, _ = gh.Viewer(ctx)
	_, _ = gh.SearchCandidates(ctx, "is:pr", "me", 10)
	_, _ = gh.SearchCandidates(ctx, "is:pr", "", 10)
	_, _ = gh.CandidateByRef(ctx, ref, "me")
	_, _, _, _ = gh.InventoryByRef(ctx, one)
	_, _, _, _ = gh.InventoryByRef(ctx, two)
	_, _ = gh.SearchStubs(ctx, "is:pr", "")
	_, _, _ = gh.SearchStubsFirst(ctx, []string{"is:pr"}, DiscoveryFirst)
	_, _ = gh.TeamMembers(ctx, "acme", "core")
	_, _ = gh.FindByBranch(ctx, []BranchQuery{{Owner: "acme", Repo: "api", Branch: "feat"}})
	_, _ = gh.Basics(ctx, ref)
	if len(queries) != 11 {
		t.Fatalf("%d documents sent, want 11", len(queries))
	}
	for _, q := range queries {
		if !strings.Contains(q, "rateLimit {") {
			t.Errorf("document without rateLimit:\n%s", q)
		}
	}
}

func TestInventoryByRefSpendsReportedCost(t *testing.T) {
	ctx, s := metertest.Start(t, context.Background())
	gh := New(runnerFunc(func() ([]byte, error) {
		return []byte(`{"data":{"rateLimit":{"cost":3,"remaining":4997,"limit":5000,"resetAt":"2026-10-05T11:00:00Z"}}}`), nil
	}))
	if _, _, _, err := gh.InventoryByRef(ctx, []Ref{{Owner: "acme", Repo: "api", Number: 1}}); err != nil {
		t.Fatal(err)
	}
	for l, want := range map[metertest.Limit]int64{
		{Res: meter.GitHub, Op: "graphql"}:                          1,
		{Res: meter.GitHub, Op: "graphql", Field: metertest.Points}: 3,
		{Res: meter.GitHub, Field: metertest.Spawns}:                0,
	} {
		if got := metertest.Count(s, l); got != want {
			t.Errorf("%s = %d, want %d", l, got, want)
		}
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

func TestStubsByRefReadsInBatchesOfForty(t *testing.T) {
	alias := regexp.MustCompile(`(pr\d+): repository\(owner: "acme", name: "api"\) \{ pullRequest\(number: (\d+)\) \{ \.\.\.stub \} \}`)
	var batches []int
	c := New(queryRunner(func(q string) ([]byte, error) {
		ms := alias.FindAllStringSubmatch(q, -1)
		batches = append(batches, len(ms))
		data := map[string]any{"rateLimit": map[string]any{"cost": 1, "remaining": 4990, "limit": 5000}}
		for _, m := range ms {
			n, _ := strconv.Atoi(m[2])
			if n == 13 {
				data[m[1]] = map[string]any{"pullRequest": nil}
				continue
			}
			data[m[1]] = map[string]any{"pullRequest": map[string]any{
				"number": n, "state": "OPEN", "isDraft": n == 2, "updatedAt": "2026-10-05T09:00:00Z", "mergeable": "CONFLICTING",
				"headRefOid": fmt.Sprintf("h%d", n), "reviewDecision": "APPROVED", "repository": map[string]any{"nameWithOwner": "acme/api"},
				"author":     map[string]any{"__typename": "User", "login": "ana"},
				"headCommit": map[string]any{"nodes": []any{map[string]any{"commit": map[string]any{"statusCheckRollup": map[string]any{"state": "FAILURE", "contexts": map[string]any{"nodes": []any{map[string]any{"name": "lint", "status": "COMPLETED", "conclusion": "FAILURE"}}}}}}}},
			}}
		}
		return json.Marshal(map[string]any{"data": data})
	}))
	var refs []Ref
	for n := 1; n <= 69; n++ {
		refs = append(refs, Ref{Owner: "acme", Repo: "api", Number: n})
	}
	stubs, errs, err := c.StubsByRef(context.Background(), refs)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(batches, []int{StubBatch, 29}) {
		t.Fatalf("batches %v", batches)
	}
	if len(stubs) != 68 || len(errs) != 1 || errs[Ref{Owner: "acme", Repo: "api", Number: 13}] == nil {
		t.Fatalf("%d stubs, errs %v", len(stubs), errs)
	}
	got := stubs[Ref{Owner: "acme", Repo: "api", Number: 2}]
	want := Stub{Ref: Ref{Owner: "acme", Repo: "api", Number: 2}, Author: "ana", UpdatedAt: time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC),
		IsDraft: true, HeadOid: "h2", ReviewDecision: "APPROVED", NameWithOwner: "acme/api", ChecksState: "FAILURE", FailedChecks: []string{"lint"}, State: "OPEN", Mergeable: "CONFLICTING"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stub\n%+v\nwant\n%+v", got, want)
	}
}

func TestThreadsKeepsEarlierBatchesWhenALaterOneFails(t *testing.T) {
	refs := map[Ref]int{}
	for n := 1; n <= 25; n++ {
		refs[Ref{Owner: "acme", Repo: "api", Number: n}] = 3
	}
	alias := regexp.MustCompile(`(pr\d+): repository`)
	calls := 0
	r := queryRunner(func(q string) ([]byte, error) {
		calls++
		if calls > 1 {
			return nil, fmt.Errorf("gh api graphql -f: HTTP 401")
		}
		data := map[string]any{}
		for _, m := range alias.FindAllStringSubmatch(q, -1) {
			data[m[1]] = map[string]any{"pullRequest": map[string]any{"reviewThreads": map[string]any{"totalCount": 1, "nodes": []any{
				map[string]any{"isResolved": false, "comments": map[string]any{"nodes": []any{map[string]any{"createdAt": "2026-10-05T09:00:00Z", "author": map[string]any{"login": "bob"}}}}},
			}}}}
		}
		return json.Marshal(map[string]any{"data": data})
	})
	threads, errs, err := New(r).Threads(context.Background(), refs)
	if err == nil || calls < 2 {
		t.Fatalf("err %v after %d requests", err, calls)
	}
	if len(threads) != 20 || len(errs) != 0 || threads[Ref{Owner: "acme", Repo: "api", Number: 1}][0].LastAuthor != "bob" {
		t.Fatalf("kept %d threads, errs %v", len(threads), errs)
	}
}
