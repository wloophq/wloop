package github

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	dgh "github.com/wloophq/wloop/internal/domain/github"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

func noSleep(ctx context.Context, _ time.Duration) error { return ctx.Err() }

func tlsClient(srv *httptest.Server, token func(context.Context) (string, error)) *Client {
	base := newTransport()
	base.TLSClientConfig = srv.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	if token == nil {
		token = func(context.Context) (string, error) { return "t", nil }
	}
	return NewClient(Options{Host: srv.URL, Base: base, Token: token, Sleep: noSleep})
}

const okQuery = `{"data":{"viewer":{"login":"me"}}}`

func TestOneConnectionForConsecutiveRequests(t *testing.T) {
	var conns atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, okQuery)
	}))
	srv.EnableHTTP2 = true
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			conns.Add(1)
		}
	}
	srv.StartTLS()
	defer srv.Close()

	c := tlsClient(srv, nil)
	for i := range 20 {
		if _, _, err := c.GraphQL(context.Background(), "query { viewer { login } }", nil); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if n := conns.Load(); n != 1 {
		t.Fatalf("20 consecutive requests opened %d connections", n)
	}
}

func TestTokenReadOnce(t *testing.T) {
	var refuse atomic.Bool
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if refuse.Load() && r.Header.Get("Authorization") == "Bearer tok-1" {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"message":"Bad credentials"}`)
			return
		}
		io.WriteString(w, okQuery)
	}))
	defer srv.Close()
	var reads atomic.Int32
	c := tlsClient(srv, func(context.Context) (string, error) {
		return fmt.Sprintf("tok-%d", reads.Add(1)), nil
	})

	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			if _, _, err := c.GraphQL(context.Background(), "{ viewer { login } }", nil); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if n := reads.Load(); n != 1 {
		t.Fatalf("50 requests read the token %d times", n)
	}

	refuse.Store(true)
	if _, _, err := c.GraphQL(context.Background(), "{ viewer { login } }", nil); err != nil {
		t.Fatalf("after a 401: %v", err)
	}
	if _, err := c.REST(context.Background(), Request{Path: "rate_limit"}); err != nil {
		t.Fatalf("with the new token: %v", err)
	}
	if n := reads.Load(); n != 2 {
		t.Fatalf("a 401 read the token %d times in all", n)
	}
}

func TestUnauthorizedAfterOneRefresh(t *testing.T) {
	var sent atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	_, err := tlsClient(srv, nil).REST(context.Background(), Request{Path: "user"})
	if !errors.Is(err, ErrUnauthorized) || sent.Load() != 2 {
		t.Fatalf("%d requests, err %v", sent.Load(), err)
	}
}

func TestGraphQLReplayedOnDeadConnection(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 2 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				conn.Close()
			}
			return
		}
		io.WriteString(w, okQuery)
	}))
	srv.StartTLS()
	defer srv.Close()

	ctx, sc := metertest.Start(t, context.Background())
	c := tlsClient(srv, nil)
	for i := range 2 {
		if _, _, err := c.GraphQL(ctx, "query { viewer { login } }", nil); err != nil {
			t.Fatalf("query %d: %v", i, err)
		}
	}
	if n := requests.Load(); n != 3 {
		t.Fatalf("the server saw %d requests, want 3: one replay", n)
	}
	metertest.Within(t, sc, metertest.Budget{{Res: meter.GitHub, Op: "graphql"}: 2})
}

func TestGraphQLRefusesMutation(t *testing.T) {
	var sent atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent.Add(1)
		io.WriteString(w, okQuery)
	}))
	defer srv.Close()
	c := tlsClient(srv, nil)

	for _, doc := range []string{
		`mutation { addComment(input: {}) { clientMutationId } }`,
		"  # a comment\n mutation M { x }",
		`subscription { x }`,
		`query { a } mutation { b }`,
		"fragment f on PullRequest { number }\nmutation { x }",
	} {
		if _, _, err := c.GraphQL(context.Background(), doc, nil); err == nil {
			t.Errorf("sent %q", doc)
		}
	}
	if n := sent.Load(); n != 0 {
		t.Fatalf("%d refused documents reached GitHub", n)
	}
	for _, doc := range []string{
		`{ viewer { login } }`,
		`query { search(query: "mutation subscription", type: ISSUE, first: 1) { issueCount } }`,
		"query Q { mutation: viewer { login } }\nfragment f on PullRequest { number }",
		`query { a(s: """mutation { x }""") }`,
	} {
		if _, _, err := c.GraphQL(context.Background(), doc, nil); err != nil {
			t.Errorf("refused %q: %v", doc, err)
		}
	}
}

func TestRequestAddsOneSample(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/graphql":
			io.WriteString(w, `{"data":{"viewer":{"login":"me"},"rateLimit":{"cost":3,"remaining":4990,"limit":5000,"resetAt":"2026-10-21T12:00:00Z"}}}`)
		case r.Header.Get("If-None-Match") == `"e1"`:
			w.WriteHeader(http.StatusNotModified)
		case r.URL.Path == "/api/v3/broken":
			w.WriteHeader(http.StatusBadGateway)
		default:
			w.Header().Set("ETag", `"e1"`)
			w.Header().Set("X-RateLimit-Resource", "core")
			w.Header().Set("X-RateLimit-Limit", "5000")
			w.Header().Set("X-RateLimit-Remaining", "4321")
			w.Header().Set("X-RateLimit-Reset", "1792584000")
			w.Header().Set("X-Poll-Interval", "60")
			io.WriteString(w, "0123456789")
		}
	}))
	defer srv.Close()
	c := tlsClient(srv, nil)

	ctx, sc := metertest.Start(t, context.Background())
	rest, err := c.REST(ctx, Request{Path: "notifications", MaxBytes: 4})
	if err != nil {
		t.Fatal(err)
	}
	if string(rest.Body) != "0123" || !rest.Truncated || rest.ETag != `"e1"` || rest.PollInterval != time.Minute ||
		rest.Limit != (dgh.Limit{Resource: dgh.ResourceCore, Limit: 5000, Remaining: 4321, ResetAt: time.Unix(1792584000, 0)}) {
		t.Fatalf("response %+v", rest)
	}
	if r, err := c.REST(ctx, Request{Path: "notifications", IfNoneMatch: `"e1"`}); err != nil || r.Status != http.StatusNotModified {
		t.Fatalf("304: %+v %v", r, err)
	}
	var se *StatusError
	if _, err := c.REST(ctx, Request{Path: "broken"}); !errors.As(err, &se) || se.Status != http.StatusBadGateway || !Transient(err) {
		t.Fatalf("502: %v", err)
	}
	if _, _, err := c.GraphQL(ctx, "{ viewer { login } rateLimit { cost remaining limit resetAt } }", nil); err != nil {
		t.Fatal(err)
	}

	rest1 := metertest.Limit{Res: meter.GitHub, Op: "rest"}
	gql := metertest.Limit{Res: meter.GitHub, Op: "graphql"}
	want := map[metertest.Limit]int64{
		rest1: 5,
		{Res: meter.GitHub, Op: "rest", Field: metertest.Points}:      4,
		{Res: meter.GitHub, Op: "rest", Field: metertest.NotModified}: 1,
		{Res: meter.GitHub, Op: "rest", Field: metertest.Errors}:      3,
		{Res: meter.GitHub, Op: "rest", Field: metertest.Bytes}:       4,
		{Res: meter.GitHub, Op: "rest", Field: metertest.Spawns}:      0,
		gql: 1,
		{Res: meter.GitHub, Op: "graphql", Field: metertest.Points}: 3,
		{Res: meter.GitHub, Op: "graphql", Field: metertest.Spawns}: 0,
	}
	for l, n := range want {
		if got := metertest.Count(sc, l); got != n {
			t.Errorf("%s = %d, want %d\n%s", l, got, n, metertest.Report(sc.Tally()))
		}
	}
	for _, s := range sc.Samples() {
		if s.Res == meter.GitHub && !strings.HasPrefix(s.Detail, "GET /") && !strings.HasPrefix(s.Detail, "POST /api/graphql") {
			t.Errorf("sample detail %q", s.Detail)
		}
	}
}

func TestNewClientDefaults(t *testing.T) {
	t.Setenv("GH_HOST", "")
	t.Setenv("GH_CONFIG_DIR", t.TempDir())
	c := NewClient(Options{Token: func(context.Context) (string, error) { return "t", nil }})
	if c.rest != "https://api.github.com" || c.graphql != "https://api.github.com/graphql" || c.timeout != 30*time.Second {
		t.Fatalf("defaults %q %q %v", c.rest, c.graphql, c.timeout)
	}
	tr, ok := c.http.Transport.(*http.Transport)
	if !ok || tr.IdleConnTimeout != 30*time.Second || tr.MaxIdleConnsPerHost != 4 || !tr.ForceAttemptHTTP2 {
		t.Fatalf("transport %+v", c.http.Transport)
	}
}

func TestHostEndpoints(t *testing.T) {
	for host, want := range map[string][2]string{
		"github.com":                 {"https://api.github.com", "https://api.github.com/graphql"},
		"GitHub.com":                 {"https://api.github.com", "https://api.github.com/graphql"},
		"ghe.example.com":            {"https://ghe.example.com/api/v3", "https://ghe.example.com/api/graphql"},
		"http://ghe.example.com:81/": {"http://ghe.example.com:81/api/v3", "http://ghe.example.com:81/api/graphql"},
	} {
		if rest, gql := endpoints(host); rest != want[0] || gql != want[1] {
			t.Errorf("%s: %s %s", host, rest, gql)
		}
	}
}

func TestDefaultHostFollowsGh(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GH_CONFIG_DIR", dir)
	t.Setenv("GH_HOST", "")
	if h := DefaultHost(); h != "github.com" {
		t.Fatalf("no hosts.yml: %s", h)
	}
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(dir, "hosts.yml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("ghe.example.com:\n    user: me\n    git_protocol: https\n")
	if h := DefaultHost(); h != "ghe.example.com" {
		t.Fatalf("one host: %s", h)
	}
	write("github.com:\n    user: me\nghe.example.com:\n    user: me\n")
	if h := DefaultHost(); h != "github.com" {
		t.Fatalf("two hosts: %s", h)
	}
	t.Setenv("GH_HOST", "ghe.example.com")
	if h := DefaultHost(); h != "ghe.example.com" {
		t.Fatalf("GH_HOST: %s", h)
	}
}

func TestEnterpriseHostLayout(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.Method+" "+r.URL.RequestURI())
		mu.Unlock()
		io.WriteString(w, `{"type":"User","data":{"viewer":{"login":"me"}}}`)
	}))
	defer srv.Close()
	c := tlsClient(srv, nil)
	if _, err := c.REST(context.Background(), Request{Path: "users/me?x=1"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.GraphQL(context.Background(), "{ viewer { login } }", nil); err != nil {
		t.Fatal(err)
	}
	if want := []string{"GET /api/v3/users/me?x=1", "POST /api/graphql"}; !slices.Equal(paths, want) {
		t.Fatalf("paths %q, want %q", paths, want)
	}
}

// blockingServer holds every request until the caller gives up; arrived
// receives once per request the handler holds.
func blockingServer(t *testing.T) (srv *httptest.Server, sent *atomic.Int32, arrived <-chan struct{}) {
	sent = new(atomic.Int32)
	arrivals := make(chan struct{}, 16)
	stop := make(chan struct{})
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent.Add(1)
		io.Copy(io.Discard, r.Body)
		select {
		case arrivals <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
		case <-stop:
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(stop) })
	return srv, sent, arrivals
}

func TestRefusedTokenRereadOncePerRecheck(t *testing.T) {
	var sent atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	base := newTransport()
	base.TLSClientConfig = srv.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	var reads atomic.Int32
	c := NewClient(Options{Host: srv.URL, Base: base, Now: func() time.Time { return now },
		Token: func(context.Context) (string, error) { reads.Add(1); return "bad", nil }})

	for range 10 {
		if _, err := c.REST(context.Background(), Request{Path: "user"}); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("err %v", err)
		}
	}
	if reads.Load() != 2 || sent.Load() != 2 {
		t.Fatalf("within the recheck: %d token reads, %d requests; want 2 and 2", reads.Load(), sent.Load())
	}
	now = now.Add(TokenRecheck)
	for range 3 {
		c.REST(context.Background(), Request{Path: "user"})
	}
	if reads.Load() != 3 || sent.Load() != 3 {
		t.Fatalf("after the recheck: %d token reads, %d requests; want 3 and 3", reads.Load(), sent.Load())
	}
}

func TestFailedTokenReadIsNotRepeated(t *testing.T) {
	var reads atomic.Int32
	c := NewClient(Options{Host: "ghe.invalid", Token: func(context.Context) (string, error) {
		reads.Add(1)
		return "", errors.New("gh: not logged in")
	}})
	for range 5 {
		if _, err := c.REST(context.Background(), Request{Path: "user"}); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("err %v", err)
		}
	}
	if n := reads.Load(); n != 1 {
		t.Fatalf("a logged-out gh was asked %d times", n)
	}
}
