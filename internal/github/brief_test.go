package github_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/github/githubtest"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func ok(r *http.Request, body io.ReadCloser) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: body, Request: r}
}

var (
	firstRE = regexp.MustCompile(`files\(first: (\d+)(?:, after: "(\d+)")?\)`)
	ref7    = github.Ref{Owner: "acme", Repo: "api", Number: 7}
)

// filesServer answers BriefFacts' queries for a pull request of n files,
// whose page cursor is the index of the page's last file.
func filesServer(t *testing.T, n int, queries *[]string) http.RoundTripper {
	return roundTrip(func(r *http.Request) (*http.Response, error) {
		var payload struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		*queries = append(*queries, payload.Query)
		m := firstRE.FindStringSubmatch(payload.Query)
		if m == nil {
			t.Fatalf("no files selection in %s", payload.Query)
		}
		first, _ := strconv.Atoi(m[1])
		from := 0
		if m[2] != "" {
			from, _ = strconv.Atoi(m[2])
		}
		to := min(from+first, n)
		var nodes []string
		for i := from; i < to; i++ {
			nodes = append(nodes, fmt.Sprintf(`{"path":"f%d.go","additions":%d,"deletions":1}`, i, i))
		}
		pr := fmt.Sprintf(`"files":{"pageInfo":{"hasNextPage":%t,"endCursor":"%d"},"nodes":[%s]}`, to < n, to, strings.Join(nodes, ","))
		if m[2] == "" {
			pr = fmt.Sprintf(`"title":"Batch export","body":"Exports.","changedFiles":%d,"closingIssuesReferences":{"nodes":[{"title":"Slow","body":"It times out."}]},`, n) + pr
		}
		body := `{"data":{"rateLimit":{"cost":1,"remaining":4990,"limit":5000,"resetAt":"2026-10-05T11:00:00Z"},"pr":{"pullRequest":{` + pr + `}}}}`
		return ok(r, io.NopCloser(strings.NewReader(body))), nil
	})
}

func TestBriefFactsIsOneCallUnder100Files(t *testing.T) {
	var queries []string
	ctx, sc := metertest.Start(t, context.Background())
	f, err := githubtest.Client(filesServer(t, 40, &queries)).BriefFacts(ctx, ref7)
	if err != nil {
		t.Fatal(err)
	}
	if got := metertest.Count(sc, metertest.Limit{Res: meter.GitHub}); got != 1 {
		t.Fatalf("%d GitHub calls, want 1", got)
	}
	if f.Title != "Batch export" || f.Body != "Exports." || f.Issue == nil || f.Issue.Title != "Slow" || f.Issue.Body != "It times out." ||
		len(f.Files) != 40 || f.Files[3] != (github.BriefFile{Path: "f3.go", Additions: 3, Deletions: 1}) || f.MoreFiles != 0 {
		t.Fatalf("%+v", f)
	}
	if !strings.Contains(queries[0], `repository(owner: "acme", name: "api") { pullRequest(number: 7)`) {
		t.Fatalf("query %s", queries[0])
	}
}

func TestBriefFactsPagesTo300Files(t *testing.T) {
	for _, c := range []struct{ files, calls, listed, more int }{
		{files: 100, calls: 1, listed: 100},
		{files: 250, calls: 3, listed: 250},
		{files: 320, calls: 3, listed: 300, more: 20},
	} {
		t.Run(strconv.Itoa(c.files), func(t *testing.T) {
			var queries []string
			ctx, sc := metertest.Start(t, context.Background())
			f, err := githubtest.Client(filesServer(t, c.files, &queries)).BriefFacts(ctx, ref7)
			if err != nil {
				t.Fatal(err)
			}
			if got := metertest.Count(sc, metertest.Limit{Res: meter.GitHub}); got != int64(c.calls) {
				t.Fatalf("%d GitHub calls, want %d", got, c.calls)
			}
			if len(f.Files) != c.listed || f.MoreFiles != c.more || f.Title != "Batch export" || f.Issue == nil {
				t.Fatalf("%d files, %d more, %+v", len(f.Files), f.MoreFiles, f.Issue)
			}
			for i, file := range f.Files {
				if file.Path != fmt.Sprintf("f%d.go", i) {
					t.Fatalf("file %d is %s", i, file.Path)
				}
			}
			for _, q := range queries[1:] {
				if strings.Contains(q, "closingIssuesReferences") {
					t.Fatalf("a later page reads the issue again: %s", q)
				}
			}
		})
	}
}

type countingBody struct {
	r      io.Reader
	read   int
	closed bool
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	b.read += n
	return n, err
}

func (b *countingBody) Close() error {
	b.closed = true
	return nil
}

func TestDiffReadsAtMostMaxPlusOneBytes(t *testing.T) {
	full := bytes.Repeat([]byte("+line\n"), 415*1024/6)
	var bodies []*countingBody
	var accept, path string
	c := githubtest.Client(roundTrip(func(r *http.Request) (*http.Response, error) {
		accept, path = r.Header.Get("Accept"), r.URL.Path
		b := &countingBody{r: bytes.NewReader(full)}
		bodies = append(bodies, b)
		return ok(r, b), nil
	}))
	diff, cut, err := c.Diff(context.Background(), ref7, 60000)
	if err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 1 || bodies[0].read != 60001 || !bodies[0].closed {
		t.Fatalf("read %d bytes of %d, closed %v", bodies[0].read, len(full), bodies[0].closed)
	}
	if !cut || !bytes.Equal(diff, full[:60000]) {
		t.Fatalf("cut %v, %d bytes", cut, len(diff))
	}
	if accept != "application/vnd.github.diff" || path != "/repos/acme/api/pulls/7" {
		t.Fatalf("accept %q, path %q", accept, path)
	}

	diff, cut, err = c.Diff(context.Background(), ref7, len(full))
	if err != nil || cut || len(diff) != len(full) {
		t.Fatalf("a diff that fits: cut %v, %d bytes, %v", cut, len(diff), err)
	}
}
