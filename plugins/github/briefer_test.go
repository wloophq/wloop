package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/wloophq/wloop/plugins/github/api"
	"github.com/wloophq/wloop/plugins/github/api/apitest"
)

// briefGitHub answers BriefFacts with facts and the diff request with diff,
// or with diffStatus and no body, and counts the requests by kind.
type briefGitHub struct {
	facts, diff string
	diffStatus  int

	mu              sync.Mutex
	graphql, rested int
}

func (g *briefGitHub) client() *api.Client {
	return apitest.Client(roundTrip(func(r *http.Request) (*http.Response, error) {
		status, body := http.StatusOK, g.diff
		g.mu.Lock()
		if r.URL.Path == "/graphql" {
			g.graphql++
			body = `{"data":{"pr":{"pullRequest":` + g.facts + `}}}`
		} else {
			g.rested++
			if g.diffStatus != 0 {
				status, body = g.diffStatus, `{"message":"diff too large"}`
			}
		}
		g.mu.Unlock()
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	}))
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const briefFacts = `{"title":"Batch export","body":"Exports in batches.","changedFiles":1,
"closingIssuesReferences":{"nodes":[{"title":"Slow export","body":"It times out."}]},
"files":{"pageInfo":{"hasNextPage":false,"endCursor":"x"},"nodes":[{"path":"export.go","additions":40,"deletions":2}]}}`

func partsOf(t *testing.T, raw json.RawMessage) BriefParts {
	t.Helper()
	var p BriefParts
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestABriefInputIsTheFactsThenTheDiffInTwoGitHubCalls(t *testing.T) {
	g := &briefGitHub{facts: briefFacts, diff: "diff --git a/export.go b/export.go\n+x\n"}
	in, err := briefInput(context.Background(), g.client(), ref7, 1000)
	if err != nil {
		t.Fatal(err)
	}
	p := partsOf(t, in.Parts)
	if p.Title != "Batch export" || len(p.Listed) != 1 || p.Listed[0] != "export.go (+40 -2)" || p.Issue != "Slow export\n\nIt times out." ||
		len(p.Files) != 1 || p.Files[0] != "export.go" || p.ChangedFiles != 1 || !strings.HasPrefix(in.Text, "diff --git") || in.Cut {
		t.Fatalf("%+v %+v", in, p)
	}
	if g.graphql != 1 || g.rested != 1 {
		t.Fatalf("%d GraphQL and %d REST calls, want one each", g.graphql, g.rested)
	}
	long := `{"title":"t","body":"` + strings.Repeat("a", bodyMax+10) + `","files":{"nodes":[]}}`
	in, _ = briefInput(context.Background(), (&briefGitHub{facts: long}).client(), ref7, 1000)
	if p := partsOf(t, in.Parts); len(p.Body) != bodyMax || p.Issue != "" {
		t.Fatalf("body %d bytes, issue %q", len(p.Body), p.Issue)
	}
}

func TestABriefInputNamesTheFilesPastTheList(t *testing.T) {
	node := `{"path":"a.go","additions":1,"deletions":0}`
	facts := `{"title":"t","body":"","changedFiles":301,"files":{"pageInfo":{"hasNextPage":true,"endCursor":"c"},"nodes":[` +
		strings.Repeat(node+",", 99) + node + `]}}`
	in, err := briefInput(context.Background(), (&briefGitHub{facts: facts}).client(), ref7, 1000)
	if err != nil {
		t.Fatal(err)
	}
	p := partsOf(t, in.Parts)
	if got := p.Listed[len(p.Listed)-1]; got != "and 1 more files" || len(p.Files) != api.BriefFilesMax {
		t.Fatalf("last line %q, %d paths", got, len(p.Files))
	}
}

func TestABriefInputCutsTheDiffOnARuneBoundary(t *testing.T) {
	diff := strings.Repeat("é", 100)
	in, err := briefInput(context.Background(), (&briefGitHub{facts: briefFacts, diff: diff}).client(), ref7, 51)
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Text) != 50 || !utf8.ValidString(in.Text) || !in.Cut || in.Bytes != 50 {
		t.Fatalf("diff %d bytes, cut %v", len(in.Text), in.Cut)
	}
	in, _ = briefInput(context.Background(), (&briefGitHub{facts: briefFacts, diff: diff}).client(), ref7, 200)
	if in.Text != diff || in.Cut {
		t.Fatalf("a diff that fits: %d bytes, cut %v", len(in.Text), in.Cut)
	}
}

func TestABriefInputNotesADiffGitHubDoesNotServe(t *testing.T) {
	in, err := briefInput(context.Background(), (&briefGitHub{facts: briefFacts, diffStatus: http.StatusNotAcceptable}).client(), ref7, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if p := partsOf(t, in.Parts); !strings.Contains(p.DiffError, "406") || in.Text != "" || p.Title != "Batch export" {
		t.Fatalf("%+v %+v", in, p)
	}
}
