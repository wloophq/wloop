package ghsource

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/github"
)

// ActionForward is the GitHub plugin's action that sends one HTTP request
// with the plugin's credentials; only the host's own code calls it.
const ActionForward = "github.forward"

// ActionBudget is the GitHub plugin's action that answers its API budget.
const ActionBudget = "github.budget"

// ActionRefresh is the GitHub plugin's action that reads a pull request in
// full into its store and reports it.
const ActionRefresh = "pr.refresh"

// exchange and answer are the plugin's api.Exchange and api.Answer.
type exchange struct {
	Method  string      `json:"method"`
	Path    string      `json:"path,omitempty"`
	GraphQL bool        `json:"graphql,omitempty"`
	Header  http.Header `json:"header,omitempty"`
	Body    []byte      `json:"body,omitempty"`
}

type answer struct {
	Status int         `json:"status"`
	Header http.Header `json:"header,omitempty"`
	Body   []byte      `json:"body,omitempty"`
}

// Forward runs the plugin's forward action with args.
type Forward func(ctx context.Context, args json.RawMessage) (json.RawMessage, error)

// PluginTransport sends every request of a GitHub client through the
// GitHub plugin, which holds the credentials: the client's own retries,
// budget and breaker stay with the client. A request the plugin could not
// answer is a 502, never a transport error, so the client sends no
// mutation twice.
type PluginTransport struct{ Forward Forward }

// graphQLPath is the GraphQL endpoint's path on the public host the
// plugin's client names.
const graphQLPath = "/graphql"

func (t PluginTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	x := exchange{Method: req.Method, Header: http.Header{}}
	for _, h := range []string{"Accept", "Content-Type", "If-None-Match", "If-Modified-Since", "X-GitHub-Api-Version"} {
		if v := req.Header.Values(h); len(v) > 0 {
			x.Header[h] = v
		}
	}
	if req.URL.Path == graphQLPath {
		x.GraphQL = true
	} else {
		x.Path = strings.TrimPrefix(req.URL.RequestURI(), "/")
	}
	if req.Body != nil {
		body, err := io.ReadAll(req.Body)
		req.Body.Close()
		if err != nil {
			return nil, err
		}
		x.Body = body
	}
	args, err := json.Marshal(x)
	if err != nil {
		return nil, err
	}
	raw, err := t.Forward(req.Context(), args)
	var a answer
	if err == nil {
		err = json.Unmarshal(raw, &a)
	}
	if err != nil {
		if req.Context().Err() != nil {
			return nil, req.Context().Err()
		}
		body, _ := json.Marshal(map[string]string{"message": fmt.Sprintf("the GitHub plugin: %v", err)})
		a = answer{Status: http.StatusBadGateway, Header: http.Header{"Content-Type": {"application/json"}}, Body: body}
	}
	if a.Header == nil {
		a.Header = http.Header{}
	}
	return &http.Response{StatusCode: a.Status, Status: http.StatusText(a.Status), Header: a.Header,
		Body: io.NopCloser(bytes.NewReader(a.Body)), ContentLength: int64(len(a.Body)), Request: req}, nil
}

// noToken is the token of a client the plugin sends for: the plugin sets
// its own.
func noToken(context.Context) (string, error) { return "plugin", nil }

// PluginClient is a GitHub API client that reaches GitHub only through the
// GitHub plugin's forward action, at now.
func PluginClient(forward Forward, now func() time.Time) *github.Client {
	return github.NewClient(github.Options{Host: "github.com", Base: PluginTransport{Forward: forward}, Token: noToken, Now: now})
}
