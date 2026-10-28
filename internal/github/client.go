package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httptrace"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"

	"github.com/wloophq/wloop/internal/brand"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	"github.com/wloophq/wloop/internal/ghcli"
	"github.com/wloophq/wloop/internal/meter"
)

type Options struct {
	// Host is the GitHub hostname as gh names it; empty is DefaultHost(). A
	// host other than github.com is served the Enterprise Server layout
	// (/api/v3 and /api/graphql); it may carry its scheme.
	Host string
	// Base is the transport; nil is HTTP/2 with IdleConnTimeout 30 s and
	// MaxIdleConnsPerHost 4.
	Base http.RoundTripper
	// Token is nil for `gh auth token --hostname <Host>`; it runs once, and
	// again after a 401 at most once per TokenRecheck.
	Token func(ctx context.Context) (string, error)
	// Floors is zero for DefaultFloors(0.5).
	Floors      Floors
	Retry       Retry
	MaxInFlight int           // 2; one slot is kept for PriorityUser
	Timeout     time.Duration // per attempt; 30 s
	Now         func() time.Time
	Sleep       func(ctx context.Context, d time.Duration) error
}

// TokenRecheck is the least time between two reads of a refused token.
const TokenRecheck = time.Minute

// Client is the loop's one connection to GitHub. Safe for concurrent use.
type Client struct {
	rest, graphql string
	http          *http.Client
	timeout       time.Duration
	token         func(ctx context.Context) (string, error)
	now           func() time.Time

	mu          sync.Mutex
	tok         string
	refused     bool // GitHub refused tok, or the last read failed
	refreshedAt time.Time

	retry  Retry
	sleep  func(ctx context.Context, d time.Duration) error
	jitter func(n int64) int64

	gate      sync.Mutex
	fails     int
	openUntil time.Time
	probing   bool
	paused    map[dgh.Resource]pause

	budget budget
	slots  slots
}

func NewClient(o Options) *Client {
	if o.Host == "" {
		o.Host = DefaultHost()
	}
	if o.Base == nil {
		o.Base = newTransport()
	}
	if o.Token == nil {
		o.Token = ghToken(o.Host)
	}
	if o.Timeout <= 0 {
		o.Timeout = 30 * time.Second
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Sleep == nil {
		o.Sleep = sleep
	}
	if o.Floors == (Floors{}) {
		o.Floors = DefaultFloors(0.5)
	}
	if o.MaxInFlight <= 0 {
		o.MaxInFlight = 2
	}
	rest, graphql := endpoints(o.Host)
	return &Client{
		rest:    rest,
		graphql: graphql,
		http:    &http.Client{Transport: o.Base},
		timeout: o.Timeout,
		token:   o.Token,
		now:     o.Now,
		retry:   o.Retry.withDefaults(),
		sleep:   o.Sleep,
		jitter:  rand.Int64N,
		paused:  map[dgh.Resource]pause{},
		budget:  budget{floors: o.Floors, buckets: map[bucketKey]dgh.Limit{}},
		slots:   newSlots(o.MaxInFlight),
	}
}

const publicHost = "github.com"

// endpoints are the REST base and the GraphQL URL of host.
func endpoints(host string) (rest, graphql string) {
	host = strings.TrimSuffix(host, "/")
	if strings.EqualFold(host, publicHost) {
		return "https://api.github.com", "https://api.github.com/graphql"
	}
	if !strings.Contains(host, "://") {
		host = "https://" + host
	}
	return host + "/api/v3", host + "/api/graphql"
}

// DefaultHost is the host gh uses when none is named: GH_HOST, else the one
// host gh's hosts.yml lists, else github.com.
func DefaultHost() string {
	if h := os.Getenv("GH_HOST"); h != "" {
		return h
	}
	if hosts := configuredHosts(); len(hosts) == 1 {
		return hosts[0]
	}
	return publicHost
}

func configuredHosts() []string {
	dir := os.Getenv("GH_CONFIG_DIR")
	if dir == "" {
		if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
			dir = filepath.Join(x, "gh")
		} else if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, ".config", "gh")
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, "hosts.yml"))
	if err != nil {
		return nil
	}
	var hosts []string
	for line := range strings.Lines(string(raw)) {
		line = strings.TrimRight(line, "\r\n")
		if line == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '#' || !strings.HasSuffix(line, ":") {
			continue
		}
		hosts = append(hosts, strings.Trim(strings.TrimSuffix(line, ":"), `"'`))
	}
	return hosts
}

func newTransport() *http.Transport {
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		IdleConnTimeout:       30 * time.Second,
		MaxIdleConnsPerHost:   4,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}

func ghToken(host string) func(ctx context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		start := time.Now()
		out, err := ghcli.New().Run(ctx, "auth", "token", "--hostname", host)
		meter.Add(ctx, meter.Sample{Res: meter.GitHub, Op: "gh auth token", Spawn: true, Dur: time.Since(start), Err: err != nil && ctx.Err() == nil})
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	}
}

// Request is a REST call.
type Request struct {
	Method, Path, Accept         string // Path from the host root, with its query
	IfNoneMatch, IfModifiedSince string
	MaxBytes                     int64 // > 0: read at most this many body bytes, then close
	Body                         []byte
	// Mutation is never retried once its request was written; only a failure
	// before the write (dial, breaker) is retried.
	Mutation bool
}

type Response struct {
	Status       int
	Body         []byte
	Truncated    bool // the body had more than MaxBytes
	ETag         string
	LastModified string
	PollInterval time.Duration
	Limit        dgh.Limit
	retryAfter   time.Duration
}

var (
	ErrRateLimited  error = &unavailable{"GitHub rate limited"}
	ErrUnavailable  error = &unavailable{"GitHub unreachable"}
	ErrUnauthorized       = errors.New("GitHub token refused")
	ErrEmpty              = errors.New("GitHub returned an empty response")
	// errTimedOut is one attempt outliving Options.Timeout while the caller's
	// context is still live, or a query GitHub stopped for time.
	errTimedOut = errors.New("GitHub timed out")
)

// unavailable is a failure on GitHub's side or on the way to it, which the
// source protocol calls src.ErrUnavailable.
type unavailable struct{ msg string }

func (e *unavailable) Error() string { return e.msg }

func (e *unavailable) Is(target error) bool { return target == src.ErrUnavailable }

// StatusError is a response GitHub answered with a status the call does not
// take as an answer.
type StatusError struct {
	Status  int
	Message string
}

func (e *StatusError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("GitHub HTTP %d", e.Status)
	}
	return fmt.Sprintf("GitHub HTTP %d: %s", e.Status, e.Message)
}

// Transient says whether err is worth a split or a later retry: 502, 503,
// 504, an attempt timeout, a GraphQL timeout, ErrUnavailable or ErrEmpty.
// The caller's own cancellation or deadline never is.
func Transient(err error) bool {
	var se *StatusError
	if errors.As(err, &se) {
		return gatewayStatus(se.Status)
	}
	return errors.Is(err, errTimedOut) || errors.Is(err, ErrUnavailable) || errors.Is(err, ErrEmpty)
}

func gatewayStatus(s int) bool {
	return s == http.StatusBadGateway || s == http.StatusServiceUnavailable || s == http.StatusGatewayTimeout
}

// REST sends r. A 304 is a Response with Status 304 and no error; any other
// status outside 2xx is a *StatusError, with the Response.
func (c *Client) REST(ctx context.Context, r Request) (Response, error) {
	method := r.Method
	if method == "" {
		method = http.MethodGet
	}
	accept := r.Accept
	if accept == "" {
		accept = "application/vnd.github+json"
	}
	path := strings.TrimPrefix(r.Path, "/")
	res := dgh.ResourceCore
	if strings.HasPrefix(path, "search/") {
		res = dgh.ResourceSearch
	}
	resp, err := c.do(ctx, exchange{op: "rest", res: res, attempts: c.retry.Attempts, mutation: r.Mutation, maxBytes: r.MaxBytes, build: func(ctx context.Context) (*http.Request, error) {
		var body io.Reader
		if r.Body != nil {
			body = bytes.NewReader(r.Body)
		}
		req, err := http.NewRequestWithContext(ctx, method, c.rest+"/"+path, body)
		if err != nil {
			return nil, err
		}
		if r.Mutation {
			// Without GetBody, net/http cannot replay the request after its
			// bytes may have reached GitHub (HTTP/2 RST_STREAM PROTOCOL_ERROR).
			req.GetBody = nil
		}
		if r.Body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Accept", accept)
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if r.IfNoneMatch != "" {
			req.Header.Set("If-None-Match", r.IfNoneMatch)
		}
		if r.IfModifiedSince != "" {
			req.Header.Set("If-Modified-Since", r.IfModifiedSince)
		}
		return req, nil
	}})
	if err != nil {
		return resp, err
	}
	if resp.Status == http.StatusNotModified || resp.Status/100 == 2 {
		return resp, nil
	}
	return resp, statusError(resp)
}

type gqlError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Path    []any  `json:"path"`
}

type gqlResponse struct {
	Data   map[string]json.RawMessage `json:"data"`
	Errors []gqlError                 `json:"errors"`
}

// GraphQL posts a read query and returns the data by top-level alias and the
// error per alias; an error with no alias in its path fails the whole call.
// It refuses a document with a mutation or a subscription. The cost a
// selected rateLimit reports goes to meter.Spend.
func (c *Client) GraphQL(ctx context.Context, query string, vars map[string]any) (map[string]json.RawMessage, map[string]string, error) {
	return c.graphQL(ctx, query, vars, c.retry.Attempts)
}

func (c *Client) graphQL(ctx context.Context, query string, vars map[string]any, attempts int) (map[string]json.RawMessage, map[string]string, error) {
	if op := writeOperation(query); op != "" {
		return nil, nil, fmt.Errorf("graphql: a %s is not a read", op)
	}
	payload := map[string]any{"query": query}
	if len(vars) > 0 {
		payload["variables"] = vars
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, err
	}
	resp, err := c.do(ctx, exchange{op: "graphql", res: dgh.ResourceGraphQL, attempts: attempts, build: func(ctx context.Context) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.graphql, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		// Present with no value: net/http may replay the POST on a dead reused
		// connection, and the header is not sent.
		req.Header["X-Idempotency-Key"] = nil
		return req, nil
	}})
	if err != nil {
		return nil, nil, err
	}
	if resp.Status != http.StatusOK {
		return nil, nil, statusError(resp)
	}
	if emptyAnswer(resp) {
		return nil, nil, fmt.Errorf("graphql: %w", ErrEmpty)
	}
	var out gqlResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil || out.Data == nil {
		if len(out.Errors) > 0 {
			return nil, nil, c.queryError(resp, out.Errors[0])
		}
		return nil, nil, fmt.Errorf("graphql: unexpected response: %.200s", resp.Body)
	}
	if raw := out.Data["rateLimit"]; len(raw) > 0 {
		var rl rawRateLimit
		if json.Unmarshal(raw, &rl) == nil {
			if rl.Cost > 0 {
				meter.Spend(ctx, meter.GitHub, "graphql", int64(rl.Cost))
			}
			c.budget.observe(dgh.Limit{Resource: dgh.ResourceGraphQL, Limit: rl.Limit, Remaining: rl.Remaining, ResetAt: rl.ResetAt}, c.now())
		}
	}
	perAlias := map[string]string{}
	for _, e := range out.Errors {
		if len(e.Path) == 0 {
			return nil, nil, c.queryError(resp, e)
		}
		if alias, ok := e.Path[0].(string); ok {
			perAlias[alias] = e.Message
		}
	}
	return out.Data, perAlias, nil
}

// send makes one attempt, and one more with a fresh token after a 401.
func (c *Client) send(ctx context.Context, x exchange) (Response, error) {
	tok, err := c.currentToken(ctx)
	if err != nil {
		return Response{}, c.tokenError(ctx, err)
	}
	resp, err := c.attempt(ctx, x, tok)
	if err != nil || resp.Status != http.StatusUnauthorized {
		return resp, err
	}
	if tok, err = c.refreshToken(ctx, tok); err != nil {
		return resp, c.tokenError(ctx, err)
	}
	resp, err = c.attempt(ctx, x, tok)
	if err == nil && resp.Status == http.StatusUnauthorized {
		c.refuse(tok)
		return resp, fmt.Errorf("%w: %v", ErrUnauthorized, statusError(resp))
	}
	return resp, err
}

func (c *Client) tokenError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, ErrUnauthorized) {
		return err
	}
	return fmt.Errorf("%w: %v", ErrUnauthorized, err)
}

// attempt sends one request. A failure of the transport or of the body read
// is the caller's ctx.Err() when the caller is done, else a *transportError.
func (c *Client) attempt(ctx context.Context, x exchange, tok string) (Response, error) {
	op := x.op
	actx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	var conn atomic.Bool
	actx = httptrace.WithClientTrace(actx, &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) { conn.Store(true) }})
	req, err := x.build(actx)
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("User-Agent", brand.Name)
	sample := meter.Sample{Res: meter.GitHub, Op: op}
	if meter.Tracing(ctx) {
		sample.Detail = cut(req.Method+" "+req.URL.Path, 300)
	}
	start := time.Now()
	hr, err := c.http.Do(req)
	if err != nil {
		sample.Dur, sample.Err = time.Since(start), ctx.Err() == nil
		meter.Add(ctx, sample)
		return Response{}, c.attemptError(ctx, actx, err, conn.Load())
	}
	resp, err := readResponse(hr, x.maxBytes)
	if err != nil {
		err = c.attemptError(ctx, actx, err, conn.Load())
	}
	sample.Dur = time.Since(start)
	sample.Bytes = int64(len(resp.Body))
	sample.NotModified = resp.Status == http.StatusNotModified
	if op == "rest" && !sample.NotModified {
		sample.Points = 1
	}
	sample.Err = resp.Status >= 500 || resp.Status == http.StatusForbidden || resp.Status == http.StatusTooManyRequests ||
		(err != nil && ctx.Err() == nil)
	meter.Add(ctx, sample)
	return resp, err
}

func (c *Client) attemptError(ctx, actx context.Context, err error, connected bool) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return &transportError{err: err, timeout: errors.Is(actx.Err(), context.DeadlineExceeded), timeoutAfter: c.timeout, connected: connected}
}

func readResponse(hr *http.Response, maxBytes int64) (Response, error) {
	defer hr.Body.Close()
	resp := Response{
		Status:       hr.StatusCode,
		ETag:         hr.Header.Get("ETag"),
		LastModified: hr.Header.Get("Last-Modified"),
		Limit:        limitOf(hr.Header),
	}
	if s, err := strconv.Atoi(hr.Header.Get("X-Poll-Interval")); err == nil {
		resp.PollInterval = time.Duration(s) * time.Second
	}
	if s, err := strconv.Atoi(hr.Header.Get("Retry-After")); err == nil {
		resp.retryAfter = time.Duration(s) * time.Second
	}
	var body io.Reader = hr.Body
	if maxBytes > 0 {
		body = io.LimitReader(hr.Body, maxBytes+1)
	}
	b, err := io.ReadAll(body)
	if maxBytes > 0 && int64(len(b)) > maxBytes {
		b, resp.Truncated = b[:maxBytes], true
	}
	resp.Body = b
	return resp, err
}

func limitOf(h http.Header) dgh.Limit {
	l := dgh.Limit{Resource: dgh.Resource(h.Get("X-RateLimit-Resource"))}
	l.Limit, _ = strconv.Atoi(h.Get("X-RateLimit-Limit"))
	l.Remaining, _ = strconv.Atoi(h.Get("X-RateLimit-Remaining"))
	if reset, err := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64); err == nil {
		l.ResetAt = time.Unix(reset, 0)
	}
	return l
}

func statusError(r Response) *StatusError {
	var body struct {
		Message string `json:"message"`
	}
	msg := ""
	if json.Unmarshal(r.Body, &body) == nil {
		msg = body.Message
	} else {
		msg = cut(strings.TrimSpace(string(r.Body)), 200)
	}
	return &StatusError{Status: r.Status, Message: msg}
}

func (c *Client) currentToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case c.refused && c.now().Sub(c.refreshedAt) < TokenRecheck:
		return "", ErrUnauthorized
	case c.refused:
		return c.readToken(ctx, true)
	case c.tok == "":
		return c.readToken(ctx, false)
	}
	return c.tok, nil
}

// refreshToken reads the token again after GitHub refused it, unless another
// caller already replaced it, or a read within TokenRecheck was refused too.
func (c *Client) refreshToken(ctx context.Context, refused string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tok != refused && c.tok != "" && !c.refused {
		return c.tok, nil
	}
	if !c.refreshedAt.IsZero() && c.now().Sub(c.refreshedAt) < TokenRecheck {
		c.refused = true
		return "", ErrUnauthorized
	}
	return c.readToken(ctx, true)
}

func (c *Client) refuse(tok string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tok == tok {
		c.refused = true
	}
}

// readToken runs with c.mu held. A failed read counts as a refusal, so a
// logged-out gh is asked again only after TokenRecheck.
func (c *Client) readToken(ctx context.Context, refresh bool) (string, error) {
	if refresh {
		c.refreshedAt = c.now()
	}
	tok, err := c.token(ctx)
	if err == nil && tok == "" {
		err = errors.New("empty token")
	}
	if err != nil {
		if ctx.Err() == nil {
			c.refreshedAt, c.refused = c.now(), true
		}
		return "", err
	}
	c.tok, c.refused = tok, false
	return tok, nil
}

// writeOperation names the first operation of doc that is not a query
// ("mutation" or "subscription"), or "" when every operation reads.
func writeOperation(doc string) string {
	depth := 0
	for i := 0; i < len(doc); i++ {
		switch ch := doc[i]; {
		case ch == '#':
			for i < len(doc) && doc[i] != '\n' {
				i++
			}
		case ch == '"':
			if strings.HasPrefix(doc[i:], `"""`) {
				end := strings.Index(doc[i+3:], `"""`)
				if end < 0 {
					return ""
				}
				i += end + 5
				continue
			}
			for i++; i < len(doc) && doc[i] != '"'; i++ {
				if doc[i] == '\\' {
					i++
				}
			}
		case ch == '{' || ch == '(':
			depth++
		case ch == '}' || ch == ')':
			depth--
		case depth == 0 && isNameStart(ch):
			j := i
			for j < len(doc) && isNameChar(doc[j]) {
				j++
			}
			if w := doc[i:j]; w == "mutation" || w == "subscription" {
				return w
			}
			i = j - 1
		}
	}
	return ""
}

func isNameStart(ch byte) bool {
	return ch == '_' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z'
}

func isNameChar(ch byte) bool { return isNameStart(ch) || ch >= '0' && ch <= '9' }

func cut(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
