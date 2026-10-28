package github

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	dgh "github.com/wloophq/wloop/internal/domain/github"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Unix(1_800_000_000, 0)} }

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func reply(r *http.Request, status int, header http.Header, body string) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

type sleeps struct {
	mu sync.Mutex
	d  []time.Duration
}

func (s *sleeps) Sleep(ctx context.Context, d time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.d = append(s.d, d)
	return ctx.Err()
}

func (s *sleeps) All() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Duration(nil), s.d...)
}

func fakeClient(rt http.RoundTripper, o Options) *Client {
	o.Host, o.Base = "https://api.github.test", rt
	o.Token = func(context.Context) (string, error) { return "t", nil }
	if o.Sleep == nil {
		o.Sleep = noSleep
	}
	return NewClient(o)
}

const viewerQuery = "query { viewer { login } }"

func TestRetryAfterPausesEveryCaller(t *testing.T) {
	clk := newClock()
	var sent atomic.Int32
	c := fakeClient(transportFunc(func(r *http.Request) (*http.Response, error) {
		if sent.Add(1) == 1 {
			return reply(r, http.StatusForbidden, http.Header{"Retry-After": {"37"}}, `{"message":"You have exceeded a secondary rate limit."}`), nil
		}
		return reply(r, http.StatusOK, nil, okQuery), nil
	}), Options{Now: clk.Now})
	ctx := context.Background()

	_, _, err := c.GraphQL(ctx, viewerQuery, nil)
	var rl *RateLimitedError
	if !errors.As(err, &rl) || !errors.Is(err, ErrRateLimited) || !rl.Secondary || rl.Resource != dgh.ResourceGraphQL ||
		!rl.RetryAt.Equal(clk.Now().Add(37*time.Second)) || sent.Load() != 1 {
		t.Fatalf("%d requests, err %v", sent.Load(), err)
	}

	var wg sync.WaitGroup
	for i := range 3 {
		ctx := ctx
		if i == 0 {
			ctx = dgh.WithPriority(ctx, dgh.PriorityBackground)
		}
		wg.Go(func() {
			if _, _, err := c.GraphQL(ctx, viewerQuery, nil); !errors.Is(err, ErrRateLimited) {
				t.Errorf("a paused call: %v", err)
			}
		})
	}
	wg.Wait()
	clk.Add(36 * time.Second)
	if _, _, err := c.GraphQL(ctx, viewerQuery, nil); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("after 36 s: %v", err)
	}
	if n := sent.Load(); n != 1 {
		t.Fatalf("%d requests during the pause", n)
	}
	if _, err := c.REST(ctx, Request{Path: "user"}); err != nil || sent.Load() != 2 {
		t.Fatalf("core is not paused: %v, %d requests", err, sent.Load())
	}
	clk.Add(time.Second)
	if _, _, err := c.GraphQL(ctx, viewerQuery, nil); err != nil || sent.Load() != 3 {
		t.Fatalf("after 37 s: %v, %d requests", err, sent.Load())
	}
}

func TestShorterPauseKeepsTheLongerOne(t *testing.T) {
	clk := newClock()
	c := fakeClient(transportFunc(func(r *http.Request) (*http.Response, error) {
		return reply(r, http.StatusOK, nil, okQuery), nil
	}), Options{Now: clk.Now})
	c.pause(dgh.ResourceGraphQL, clk.Now().Add(100*time.Second), false)
	if e := c.pause(dgh.ResourceGraphQL, clk.Now().Add(10*time.Second), true); !e.RetryAt.Equal(clk.Now().Add(100*time.Second)) || e.Secondary {
		t.Fatalf("a shorter pause reported %+v", e)
	}
}

func TestPrimaryExhaustionWaitsForReset(t *testing.T) {
	clk := newClock()
	reset := clk.Now().Add(10 * time.Minute)
	exhausted := func(res dgh.Resource) http.Header {
		return http.Header{
			"X-Ratelimit-Resource":  {string(res)},
			"X-Ratelimit-Limit":     {"5000"},
			"X-Ratelimit-Remaining": {"0"},
			"X-Ratelimit-Reset":     {strconv.FormatInt(reset.Unix(), 10)},
		}
	}
	var sent atomic.Int32
	c := fakeClient(transportFunc(func(r *http.Request) (*http.Response, error) {
		n := sent.Add(1)
		switch {
		case n == 1:
			return reply(r, http.StatusForbidden, exhausted(dgh.ResourceCore), `{"message":"API rate limit exceeded for user ID 1."}`), nil
		case n == 2:
			return reply(r, http.StatusOK, exhausted(dgh.ResourceGraphQL), `{"errors":[{"type":"RATE_LIMITED","message":"API rate limit already exceeded for user ID 1."}]}`), nil
		}
		return reply(r, http.StatusOK, nil, okQuery), nil
	}), Options{Now: clk.Now})
	ctx := context.Background()

	_, err := c.REST(ctx, Request{Path: "user"})
	var rl *RateLimitedError
	if !errors.As(err, &rl) || rl.Secondary || rl.Resource != dgh.ResourceCore || !rl.RetryAt.Equal(reset) {
		t.Fatalf("403 with no remaining: %v", err)
	}
	_, _, err = c.GraphQL(ctx, viewerQuery, nil)
	if !errors.As(err, &rl) || rl.Secondary || rl.Resource != dgh.ResourceGraphQL || !rl.RetryAt.Equal(reset) {
		t.Fatalf("RATE_LIMITED: %v", err)
	}
	clk.Add(10*time.Minute - time.Second)
	if _, err := c.REST(ctx, Request{Path: "user"}); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("core before reset: %v", err)
	}
	if _, _, err := c.GraphQL(ctx, viewerQuery, nil); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("graphql before reset: %v", err)
	}
	if n := sent.Load(); n != 2 {
		t.Fatalf("%d requests before the reset", n)
	}
	clk.Add(time.Second)
	if _, err := c.REST(ctx, Request{Path: "user"}); err != nil {
		t.Fatalf("core at reset: %v", err)
	}
	if _, _, err := c.GraphQL(ctx, viewerQuery, nil); err != nil || sent.Load() != 4 {
		t.Fatalf("graphql at reset: %v, %d requests", err, sent.Load())
	}
}

func TestBackoffFullJitterBounds(t *testing.T) {
	c := NewClient(Options{Token: func(context.Context) (string, error) { return "t", nil }})
	for n := range 8 {
		ceiling := min(30*time.Second, time.Second<<n)
		lo, hi := ceiling, time.Duration(0)
		for range 2000 {
			d := c.backoff(n)
			if d < 0 || d >= ceiling {
				t.Fatalf("retry %d slept %v, outside [0, %v)", n+1, d, ceiling)
			}
			lo, hi = min(lo, d), max(hi, d)
		}
		if lo > ceiling/10 || hi < ceiling*9/10 {
			t.Fatalf("retry %d: sleeps in [%v, %v] do not span [0, %v)", n+1, lo, hi, ceiling)
		}
	}

	var sent atomic.Int32
	var s sleeps
	c = fakeClient(transportFunc(func(r *http.Request) (*http.Response, error) {
		sent.Add(1)
		return reply(r, http.StatusBadGateway, nil, ""), nil
	}), Options{Sleep: s.Sleep})
	_, err := c.REST(context.Background(), Request{Path: "user"})
	var se *StatusError
	got := s.All()
	if !errors.As(err, &se) || se.Status != http.StatusBadGateway || sent.Load() != 3 || len(got) != 2 ||
		got[0] >= time.Second || got[1] >= 2*time.Second {
		t.Fatalf("%d requests, sleeps %v, err %v", sent.Load(), got, err)
	}
}

func TestBreakerOpensAfterThreeNetworkFailures(t *testing.T) {
	clk := newClock()
	var sent atomic.Int32
	var up atomic.Bool
	entered, hold := make(chan struct{}, 1), make(chan struct{})
	c := fakeClient(transportFunc(func(r *http.Request) (*http.Response, error) {
		sent.Add(1)
		if !up.Load() {
			return nil, errors.New("connection reset by peer")
		}
		entered <- struct{}{}
		<-hold
		return reply(r, http.StatusOK, nil, okQuery), nil
	}), Options{Now: clk.Now, Retry: Retry{Attempts: 1}})
	ctx := context.Background()

	for i := range 3 {
		if _, _, err := c.GraphQL(ctx, viewerQuery, nil); !errors.Is(err, ErrUnavailable) || !Transient(err) {
			t.Fatalf("failure %d: %v", i+1, err)
		}
	}
	if _, err := c.REST(ctx, Request{Path: "user"}); !errors.Is(err, ErrUnavailable) || sent.Load() != 3 {
		t.Fatalf("4th call: %d requests, err %v", sent.Load(), err)
	}
	clk.Add(59 * time.Second)
	if _, _, err := c.GraphQL(ctx, viewerQuery, nil); !errors.Is(err, ErrUnavailable) || sent.Load() != 3 {
		t.Fatalf("after 59 s: %d requests, err %v", sent.Load(), err)
	}

	clk.Add(time.Second)
	up.Store(true)
	probe := make(chan error)
	go func() {
		_, _, err := c.GraphQL(ctx, viewerQuery, nil)
		probe <- err
	}()
	<-entered
	if _, _, err := c.GraphQL(ctx, viewerQuery, nil); !errors.Is(err, ErrUnavailable) || sent.Load() != 4 {
		t.Fatalf("beside the probe: %d requests, err %v", sent.Load(), err)
	}
	close(hold)
	if err := <-probe; err != nil {
		t.Fatalf("probe: %v", err)
	}
	if _, _, err := c.GraphQL(ctx, viewerQuery, nil); err != nil || sent.Load() != 5 {
		t.Fatalf("after the probe: %d requests, err %v", sent.Load(), err)
	}

	sent.Store(0)
	c = fakeClient(transportFunc(func(*http.Request) (*http.Response, error) {
		sent.Add(1)
		return nil, io.ErrUnexpectedEOF
	}), Options{Now: clk.Now})
	c.GraphQL(ctx, viewerQuery, nil)
	if _, _, err := c.GraphQL(ctx, viewerQuery, nil); !errors.Is(err, ErrUnavailable) || sent.Load() != 3 {
		t.Fatalf("one call's three attempts did not open the breaker: %d requests, err %v", sent.Load(), err)
	}
}

func TestBreakerLetsOneProbeThrough(t *testing.T) {
	clk := newClock()
	var sent atomic.Int32
	entered, hold := make(chan string, 2), make(chan struct{})
	c := fakeClient(transportFunc(func(r *http.Request) (*http.Response, error) {
		sent.Add(1)
		switch path := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]; path {
		case "fail":
			return nil, errors.New("connection reset by peer")
		case "slow", "probe":
			entered <- path
			select {
			case <-hold:
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		}
		return reply(r, http.StatusOK, nil, `{}`), nil
	}), Options{Now: clk.Now, Retry: Retry{Attempts: 1}})
	ctx := context.Background()

	sctx, cancelSlow := context.WithCancel(ctx)
	slow := make(chan error)
	go func() {
		_, err := c.REST(sctx, Request{Path: "slow"})
		slow <- err
	}()
	<-entered
	for range 3 {
		c.REST(ctx, Request{Path: "fail"})
	}
	clk.Add(time.Minute)
	probe := make(chan error)
	go func() {
		_, err := c.REST(ctx, Request{Path: "probe"})
		probe <- err
	}()
	<-entered
	cancelSlow()
	if err := <-slow; !errors.Is(err, context.Canceled) {
		t.Fatalf("slow call: %v", err)
	}
	if _, err := c.REST(ctx, Request{Path: "other"}); !errors.Is(err, ErrUnavailable) || sent.Load() != 5 {
		t.Fatalf("a call that ended beside the probe let a second one through: %d requests, err %v", sent.Load(), err)
	}
	close(hold)
	if err := <-probe; err != nil {
		t.Fatalf("probe: %v", err)
	}
}

func TestAttemptTimeoutIsTransient(t *testing.T) {
	var sent atomic.Int32
	var s sleeps
	var cancelOnSend atomic.Pointer[context.CancelFunc]
	c := fakeClient(transportFunc(func(r *http.Request) (*http.Response, error) {
		sent.Add(1)
		if cancel := cancelOnSend.Load(); cancel != nil {
			(*cancel)()
		}
		<-r.Context().Done()
		return nil, r.Context().Err()
	}), Options{Timeout: 20 * time.Millisecond, Retry: Retry{Attempts: 2}, Sleep: s.Sleep})

	_, _, err := c.GraphQL(context.Background(), viewerQuery, nil)
	if !Transient(err) || !errors.Is(err, ErrUnavailable) || errors.Is(err, context.DeadlineExceeded) ||
		!strings.Contains(err.Error(), "timed out after 20ms") || sent.Load() != 2 || len(s.All()) != 1 {
		t.Fatalf("%d requests, %d sleeps, err %v", sent.Load(), len(s.All()), err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancelOnSend.Store(&cancel)
	_, _, err = c.GraphQL(ctx, viewerQuery, nil)
	if Transient(err) || !errors.Is(err, context.Canceled) || sent.Load() != 3 || len(s.All()) != 1 {
		t.Fatalf("the caller's cancel: %d requests, err %v", sent.Load(), err)
	}

	srv, _, _ := blockingServer(t)
	base := newTransport()
	base.TLSClientConfig = srv.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	c = NewClient(Options{Host: srv.URL, Base: base, Timeout: 50 * time.Millisecond, Retry: Retry{Attempts: 1}, Sleep: noSleep,
		Token: func(context.Context) (string, error) { return "t", nil }})
	if _, _, err := c.GraphQL(context.Background(), viewerQuery, nil); !Transient(err) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("an attempt timeout over TLS: %v, transient %v", err, Transient(err))
	}
}

func TestMutationIsNeverRetriedAfterWrite(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		io.Copy(io.Discard, r.Body)
		switch strings.TrimPrefix(r.URL.Path, "/api/v3") {
		case "/drop":
			if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
				conn.Close()
			}
		case "/gateway":
			w.WriteHeader(http.StatusBadGateway)
		default:
			w.WriteHeader(http.StatusCreated)
			io.WriteString(w, `{}`)
		}
	}))
	defer srv.Close()
	var dials atomic.Int32
	base := newTransport()
	dial := base.DialContext
	base.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if dials.Add(1) == 1 {
			return nil, &net.OpError{Op: "dial", Net: network, Err: errors.New("connection refused")}
		}
		return dial(ctx, network, addr)
	}
	c := NewClient(Options{Host: srv.URL, Base: base, Token: func(context.Context) (string, error) { return "t", nil }, Sleep: noSleep})
	ctx := context.Background()
	post := func(path string, mutation bool) (Response, error) {
		hits.Store(0)
		return c.REST(ctx, Request{Method: http.MethodPost, Path: path, Body: []byte(`{"body":"hi"}`), Mutation: mutation})
	}

	if resp, err := post("repos/acme/api/issues/1/comments", true); err != nil || resp.Status != http.StatusCreated || hits.Load() != 1 || dials.Load() != 2 {
		t.Fatalf("a refused dial: %d hits, %d dials, err %v", hits.Load(), dials.Load(), err)
	}
	if _, err := post("drop", true); err == nil || hits.Load() != 1 {
		t.Fatalf("a connection lost after the write: %d hits, err %v", hits.Load(), err)
	}
	var se *StatusError
	if _, err := post("gateway", true); !errors.As(err, &se) || hits.Load() != 1 {
		t.Fatalf("a 502: %d hits, err %v", hits.Load(), err)
	}
	if _, err := post("gateway", false); !errors.As(err, &se) || hits.Load() != 3 {
		t.Fatalf("a read posted to the same 502: %d hits, err %v", hits.Load(), err)
	}

	var replayable []bool
	rc := fakeClient(transportFunc(func(r *http.Request) (*http.Response, error) {
		replayable = append(replayable, r.GetBody != nil)
		return reply(r, http.StatusCreated, nil, `{}`), nil
	}), Options{})
	for _, mutation := range []bool{true, false} {
		if _, err := rc.REST(ctx, Request{Method: http.MethodPost, Path: "repos/acme/api/issues/1/comments", Body: []byte(`{}`), Mutation: mutation}); err != nil {
			t.Fatal(err)
		}
	}
	if fmt.Sprint(replayable) != "[false true]" {
		t.Fatalf("GetBody set on [mutation read]: %v; net/http may replay a mutation", replayable)
	}
}

func emptyThen(bodies ...string) (http.RoundTripper, *atomic.Int32) {
	var sent atomic.Int32
	return transportFunc(func(r *http.Request) (*http.Response, error) {
		n := int(sent.Add(1)) - 1
		if n < len(bodies) && bodies[n] == "network" {
			return nil, errors.New("connection reset by peer")
		}
		body := okQuery
		if n < len(bodies) {
			body = bodies[n]
		}
		return reply(r, http.StatusOK, nil, body), nil
	}), &sent
}

func TestEmptyGraphQLAnswerIsRetriedOnce(t *testing.T) {
	rt, sent := emptyThen("")
	var s sleeps
	c := fakeClient(rt, Options{Sleep: s.Sleep, Retry: Retry{Attempts: 1}})
	c.fails = 2
	if _, _, err := c.GraphQL(context.Background(), viewerQuery, nil); err != nil {
		t.Fatalf("empty then ok: %v", err)
	}
	if sent.Load() != 2 || len(s.All()) != 1 {
		t.Fatalf("%d requests, sleeps %v; want one retry after one backoff", sent.Load(), s.All())
	}
	if c.fails != 0 {
		t.Fatalf("breaker failures %d after the answer", c.fails)
	}
}

func TestEmptyGraphQLAnswerTwiceIsOneEmptyError(t *testing.T) {
	rt, sent := emptyThen("", " \n", "")
	c := fakeClient(rt, Options{Retry: Retry{Attempts: 3}})
	c.fails = 2
	_, _, err := c.GraphQL(context.Background(), viewerQuery, nil)
	if !errors.Is(err, ErrEmpty) || errors.Is(err, ErrUnavailable) || !Transient(err) {
		t.Fatalf("err %v; want a transient ErrEmpty, not ErrUnavailable", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "GitHub returned an empty response") || strings.Contains(msg, "unreachable") {
		t.Fatalf("message %q", msg)
	}
	if sent.Load() != 2 {
		t.Fatalf("%d requests, want 2", sent.Load())
	}
	if c.fails != 2 {
		t.Fatalf("breaker failures %d, want the 2 it had", c.fails)
	}
	if _, _, err := c.GraphQL(context.Background(), viewerQuery, nil); errors.Is(err, errBreaker) {
		t.Fatalf("empty answers opened the breaker: %v", err)
	}
}

func TestNetworkFailureStillFeedsTheBreaker(t *testing.T) {
	rt, sent := emptyThen("network", "network", "network")
	c := fakeClient(rt, Options{Retry: Retry{Attempts: 3}})
	if _, _, err := c.GraphQL(context.Background(), viewerQuery, nil); !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrEmpty) {
		t.Fatalf("err %v", err)
	}
	if _, _, err := c.GraphQL(context.Background(), viewerQuery, nil); !errors.Is(err, errBreaker) || sent.Load() != 3 {
		t.Fatalf("%d requests, err %v; want the breaker open after 3 network failures", sent.Load(), err)
	}
}
