// Package githubtest serves a github.Client from the ghcli.Runner fakes the
// tests already have.
package githubtest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/ghcli"
	"github.com/wloophq/wloop/internal/github"
)

// RunnerTransport answers POST /graphql by passing the query to R as
// `api graphql -f query=<q>` and every other request as `api <path>`. A
// runner error naming "HTTP <status>" answers that status with the runner's
// output; any other error answers 200 with the output when there is some, as
// gh prints a GraphQL response with errors, and fails the round trip when
// there is none.
type RunnerTransport struct{ R ghcli.Runner }

var statusRE = regexp.MustCompile(`HTTP (\d{3})`)

func (t RunnerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	args := []string{"api", strings.TrimPrefix(req.URL.RequestURI(), "/")}
	if req.Method == http.MethodPost && req.URL.Path == "/graphql" {
		var payload struct {
			Query string `json:"query"`
		}
		if req.Body != nil {
			raw, err := io.ReadAll(req.Body)
			req.Body.Close()
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(raw, &payload); err != nil {
				return nil, err
			}
		}
		args = []string{"api", "graphql", "-f", "query=" + payload.Query}
	}
	out, err := t.R.Run(req.Context(), args...)
	status := http.StatusOK
	if err != nil {
		m := statusRE.FindStringSubmatch(err.Error())
		switch {
		case m != nil:
			status, _ = strconv.Atoi(m[1])
		case len(out) == 0:
			return nil, err
		}
	}
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(out)),
		Request:    req,
	}, nil
}

// Client builds a Client over t with a fixed token and no backoff sleeps.
func Client(t http.RoundTripper) *github.Client {
	return github.NewClient(github.Options{
		Host:  "github.com",
		Base:  t,
		Token: func(context.Context) (string, error) { return "test-token", nil },
		Sleep: func(ctx context.Context, _ time.Duration) error { return ctx.Err() },
	})
}

// Runner is Client over RunnerTransport{r}.
func Runner(r ghcli.Runner) *github.Client { return Client(RunnerTransport{r}) }
