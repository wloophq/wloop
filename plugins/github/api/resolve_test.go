package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type sentQuery struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

// graphQLServer answers every GraphQL POST with status and answer, and
// records each request's query and variables.
type graphQLServer struct {
	mu     sync.Mutex
	status int
	answer string
	got    []sentQuery
}

func (s *graphQLServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Method != http.MethodPost || r.URL.Path != "/api/graphql" {
		http.NotFound(w, r)
		return
	}
	raw, _ := io.ReadAll(r.Body)
	var in sentQuery
	_ = json.Unmarshal(raw, &in)
	s.got = append(s.got, in)
	if s.status != 0 {
		w.WriteHeader(s.status)
	}
	io.WriteString(w, s.answer)
}

func (s *graphQLServer) answerWith(status int, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status, s.answer, s.got = status, body, nil
}

func (s *graphQLServer) sent() []sentQuery {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sentQuery(nil), s.got...)
}

func TestResolveExchange(t *testing.T) {
	ctx := context.Background()
	s := &graphQLServer{answer: `{"data":{"resolveReviewThread":{"thread":{"id":"PRRT_kwDOAbCdEf4AAAAB","isResolved":true}}}}`}
	srv := httptest.NewTLSServer(s)
	defer srv.Close()
	c := tlsClient(srv, nil)

	if err := c.ResolveReviewThread(ctx, "PRRT_kwDOAbCdEf4AAAAB"); err != nil {
		t.Fatal(err)
	}
	if got := s.sent(); len(got) != 1 || !strings.Contains(got[0].Query, "resolveReviewThread") || got[0].Variables["id"] != "PRRT_kwDOAbCdEf4AAAAB" {
		t.Fatalf("sent %+v", got)
	}

	for name, a := range map[string]struct {
		status int
		body   string
	}{
		"a gateway status":      {http.StatusBadGateway, `{}`},
		"an empty answer":       {0, ``},
		"an error":              {0, `{"data":{"resolveReviewThread":null},"errors":[{"type":"NOT_FOUND","message":"Could not resolve to a node","path":["resolveReviewThread"]}]}`},
		"a thread not resolved": {0, `{"data":{"resolveReviewThread":{"thread":{"id":"PRRT_x","isResolved":false}}}}`},
	} {
		s.answerWith(a.status, a.body)
		if err := c.ResolveReviewThread(ctx, "PRRT_x"); err == nil {
			t.Errorf("%s: resolved", name)
		}
		if n := len(s.sent()); n != 1 {
			t.Errorf("%s: sent the mutation %d times", name, n)
		}
	}
	s.answerWith(0, okQuery)
	if err := c.ResolveReviewThread(ctx, ""); err == nil || len(s.sent()) != 0 {
		t.Fatalf("resolved no thread: %v", err)
	}
}

func TestGraphQLRefusesOtherMutations(t *testing.T) {
	s := &graphQLServer{answer: okQuery}
	srv := httptest.NewTLSServer(s)
	defer srv.Close()
	c := tlsClient(srv, nil)
	for _, doc := range []string{
		resolveThreadMutation,
		`mutation { addPullRequestReview(input: {pullRequestId: "PR_x", event: APPROVE}) { clientMutationId } }`,
		`mutation { unresolveReviewThread(input: {threadId: "PRRT_x"}) { thread { isResolved } } }`,
		`mutation { addPullRequestReviewThreadReply(input: {pullRequestReviewThreadId: "PRRT_x", body: "x"}) { clientMutationId } }`,
	} {
		if _, _, err := c.GraphQL(context.Background(), doc, nil); err == nil {
			t.Errorf("sent %q", doc)
		}
	}
	if n := len(s.sent()); n != 0 {
		t.Fatalf("%d mutations reached GitHub through GraphQL", n)
	}
}
