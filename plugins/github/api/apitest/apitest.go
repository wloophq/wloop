// Package apitest serves an api.Client from gh fakes, as the plugin's
// tests write them, and records what it meters.
package apitest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wloophq/wloop/plugins/github/api"
	"github.com/wloophq/wloop/plugins/github/gh"
)

// RunnerTransport answers POST /graphql by passing the query to R as
// `api graphql -f query=<q>` and every other request as `api <path>`. A
// runner error naming "HTTP <status>" answers that status with the runner's
// output; any other error answers 200 with the output when there is some, as
// gh prints a GraphQL response with errors, and fails the round trip when
// there is none.
type RunnerTransport struct{ R gh.Runner }

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
func Client(t http.RoundTripper) *api.Client {
	return api.NewClient(api.Options{
		Host:  "github.com",
		Base:  t,
		Token: func(context.Context) (string, error) { return "test-token", nil },
		Sleep: func(ctx context.Context, _ time.Duration) error { return ctx.Err() },
	})
}

// Runner is Client over RunnerTransport{r}.
func Runner(r gh.Runner) *api.Client { return Client(RunnerTransport{r}) }

// Meter records the samples a client meters, safe for concurrent use.
type Meter struct {
	mu      sync.Mutex
	samples []api.Sample
}

// Hear is the Meter a client's Options take.
func (m *Meter) Hear(_ context.Context, s api.Sample) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.samples = append(m.samples, s)
}

// Samples are the samples heard, in order.
func (m *Meter) Samples() []api.Sample {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.samples)
}

// Stat is what the samples of one op add up to: Calls counts requests,
// not the costs a response reported.
type Stat struct {
	Calls, Spawns, Errors, NotModified, Points, Bytes int64
}

// Of is what the samples of op add up to; "" is every op.
func (m *Meter) Of(op string) Stat {
	var st Stat
	for _, s := range m.Samples() {
		if op != "" && s.Op != op {
			continue
		}
		st.Points += s.Points
		if s.Spend {
			continue
		}
		st.Calls++
		st.Bytes += s.Bytes
		if s.Spawn {
			st.Spawns++
		}
		if s.Failed {
			st.Errors++
		}
		if s.NotModified {
			st.NotModified++
		}
	}
	return st
}

// Metered is Client over t, metering into m.
func Metered(t http.RoundTripper, m *Meter) *api.Client {
	return api.NewClient(api.Options{
		Host:  "github.com",
		Base:  t,
		Token: func(context.Context) (string, error) { return "test-token", nil },
		Sleep: func(ctx context.Context, _ time.Duration) error { return ctx.Err() },
		Meter: m.Hear,
	})
}

// Options are Client's options over r: a fixed token and no backoff sleeps.
func Options(r gh.Runner) api.Options {
	return api.Options{Host: "github.com", Base: RunnerTransport{r}, Token: func(context.Context) (string, error) { return "test-token", nil },
		Sleep: func(ctx context.Context, _ time.Duration) error { return ctx.Err() }}
}
