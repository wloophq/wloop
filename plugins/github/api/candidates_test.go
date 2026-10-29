package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	. "github.com/wloophq/wloop/plugins/github/api"
)

func lit(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
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
	want := Candidate{Ref: Ref{Owner: "acme", Repo: "api", Number: 7}, URL: "https://github.com/acme/api/pull/7", Title: "Batch export",
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

func TestSearchCandidatesEachIsOneRequest(t *testing.T) {
	node := candidatePage[strings.Index(candidatePage, `{"number":7`):strings.Index(candidatePage, ",\n{}")]
	resp := `{"data":{"rateLimit":{"cost":3,"remaining":4990,"limit":5000,"resetAt":"2026-10-05T11:00:00Z"},
"s0":{"issueCount":0,"nodes":[]},
"s1":{"issueCount":4,"nodes":[` + node + `]}}}`
	var queries []string
	c := New(queryRunner(func(q string) ([]byte, error) {
		queries = append(queries, q)
		return []byte(resp), nil
	}))
	qs := []string{`author:ana repo:acme/web`, `author:ana repo:acme/"api"`}
	pages, errs, err := c.SearchCandidatesEach(context.Background(), qs, "me", 30)
	if err != nil || errs[0] != nil || errs[1] != nil {
		t.Fatal(err, errs)
	}
	if len(queries) != 1 {
		t.Fatalf("%d requests, want 1", len(queries))
	}
	for i, q := range qs {
		if want := fmt.Sprintf("s%d: search(query: %s, type: ISSUE, first: 30)", i, lit(q)); !strings.Contains(queries[0], want) {
			t.Fatalf("no alias %q in:\n%s", want, queries[0])
		}
	}
	if len(pages) != 2 || pages[0].Total != 0 || len(pages[0].Candidates) != 0 || pages[1].Total != 4 ||
		len(pages[1].Candidates) != 1 || pages[1].Candidates[0].Ref != (Ref{Owner: "acme", Repo: "api", Number: 7}) || pages[1].RateLimit.Cost != 3 {
		t.Fatalf("pages %+v", pages)
	}

	failed := `{"data":{"s0":{"issueCount":0,"nodes":[]},"s1":null},"errors":[{"path":["s1"],"message":"search timed out"}]}`
	c = New(queryRunner(func(string) ([]byte, error) { return []byte(failed), nil }))
	pages, errs, err = c.SearchCandidatesEach(context.Background(), qs, "me", 30)
	if err != nil || errs[0] != nil || errs[1] == nil || !strings.Contains(errs[1].Error(), "search timed out") || len(pages) != 2 {
		t.Fatalf("a failed alias: %v %v", errs, err)
	}
}
