package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
)

// Exchange is one HTTP request a caller outside the plugin builds and the
// plugin sends with its credentials: Path is relative to the REST root, or
// empty with GraphQL set for the GraphQL endpoint.
type Exchange struct {
	Method  string      `json:"method"`
	Path    string      `json:"path,omitempty"`
	GraphQL bool        `json:"graphql,omitempty"`
	Header  http.Header `json:"header,omitempty"`
	Body    []byte      `json:"body,omitempty"`
}

// Answer is what GitHub answered an Exchange.
type Answer struct {
	Status int         `json:"status"`
	Header http.Header `json:"header,omitempty"`
	Body   []byte      `json:"body,omitempty"`
}

// forwarded are the headers of an Exchange the plugin sends on; it sets
// the credentials and the user agent itself.
var forwarded = []string{"Accept", "Content-Type", "If-None-Match", "If-Modified-Since", "X-GitHub-Api-Version"}

// Forward sends x once with the plugin's token, again with a fresh one
// after a 401, and keeps the budget its answer reports. It neither retries
// nor holds the budget's floors: the caller's own client does both.
func (c *Client) Forward(ctx context.Context, x Exchange) (Answer, error) {
	res, op, url := ResourceCore, "rest", c.rest+"/"+strings.TrimPrefix(x.Path, "/")
	switch {
	case x.GraphQL:
		res, op, url = ResourceGraphQL, "graphql", c.graphql
	case strings.HasPrefix(strings.TrimPrefix(x.Path, "/"), "search/"):
		res = ResourceSearch
	}
	ex := exchange{op: op, res: res, build: func(ctx context.Context) (*http.Request, error) {
		var body io.Reader
		if x.Body != nil {
			body = bytes.NewReader(x.Body)
		}
		req, err := http.NewRequestWithContext(ctx, x.Method, url, body)
		if err != nil {
			return nil, err
		}
		req.GetBody = nil
		for _, h := range forwarded {
			if v := x.Header.Values(h); len(v) > 0 {
				req.Header[h] = v
			}
		}
		return req, nil
	}}
	resp, err := c.send(ctx, ex)
	c.observe(res, resp.Limit)
	if err != nil {
		return Answer{}, err
	}
	return Answer{Status: resp.Status, Header: resp.header, Body: resp.Body}, nil
}
