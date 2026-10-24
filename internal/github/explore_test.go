package github_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	. "github.com/wloophq/wloop/internal/github"
)

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
