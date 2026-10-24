// Package slack is a thin client for the Slack Web API.
package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const DefaultBaseURL = "https://slack.com/api/"

// defaultRetryAfter applies when a 429 carries no usable Retry-After.
const defaultRetryAfter = 30 * time.Second

// defaultHTTP never follows a redirect: a 307/308 would resend the form, tokens included, elsewhere.
var defaultHTTP = &http.Client{
	Timeout:       30 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

type TokenSource interface {
	// Token returns a valid access token, refreshing it when needed.
	Token(ctx context.Context) (string, error)
}

type Client struct {
	BaseURL string // "https://slack.com/api/" by default; tests point it at fake Slack
	HTTP    *http.Client
	Tokens  TokenSource // the user token; ConnectionsOpen takes the app token explicitly
	Limits  *Limiter    // per method, honors Retry-After; nil uses the default tiers
	// RequestLog, when set, gets one call per request: method and HTTP status only,
	// never args, bodies, URLs with queries or tokens. Config slack.request_log turns it on.
	RequestLog func(method string, status int)
	// OnRateLimited, when set, is called once per HTTP 429 with the method name.
	OnRateLimited func(method string)

	once     sync.Once
	fallback *Limiter
}

// Call posts form-encoded args to method and decodes the JSON body into out.
// A body with "ok": false becomes *APIError; HTTP 429 waits Retry-After once and retries.
func (c *Client) Call(ctx context.Context, method string, args url.Values, out any) error {
	_, err := c.call(ctx, method, c.userToken, args, out)
	return err
}

type APIError struct {
	Method, Code     string
	Needed, Provided string
}

// Error is "slack <method>: <code>", plus the needed scope for missing_scope; never the args.
func (e *APIError) Error() string {
	if e.Needed != "" {
		return "slack " + e.Method + ": " + e.Code + " (needed " + e.Needed + ")"
	}
	return "slack " + e.Method + ": " + e.Code
}

type bearer func(ctx context.Context) (string, error)

func (c *Client) userToken(ctx context.Context) (string, error) {
	if c.Tokens == nil {
		return "", errors.New("no token source")
	}
	return c.Tokens.Token(ctx)
}

func fixedToken(tok string) bearer {
	return func(context.Context) (string, error) { return tok, nil }
}

func noToken(context.Context) (string, error) { return "", nil }

type envelope struct {
	OK       bool   `json:"ok"`
	Error    string `json:"error"`
	Needed   string `json:"needed"`
	Provided string `json:"provided"`
}

// call returns the response header of the request that answered.
func (c *Client) call(ctx context.Context, method string, token bearer, args url.Values, out any) (http.Header, error) {
	limits := c.limiter()
	for attempt := 0; ; attempt++ {
		if err := limits.Wait(ctx, method); err != nil {
			return nil, fmt.Errorf("slack %s: %w", method, err)
		}
		status, header, body, err := c.post(ctx, method, token, args)
		if err != nil {
			return nil, err
		}
		var env envelope
		if status/100 == 2 {
			if err := json.Unmarshal(body, &env); err != nil {
				return nil, fmt.Errorf("slack %s: HTTP %d, body is not JSON", method, status)
			}
		}
		if status == http.StatusTooManyRequests || (status/100 == 2 && !env.OK && env.Error == "ratelimited") {
			if c.OnRateLimited != nil {
				c.OnRateLimited(method)
			}
			limits.hold(method, retryAfter(header))
			if attempt == 0 {
				continue
			}
			return nil, &APIError{Method: method, Code: "ratelimited"}
		}
		if status/100 != 2 {
			return nil, fmt.Errorf("slack %s: HTTP %d", method, status)
		}
		if !env.OK {
			code := env.Error
			if code == "" {
				code = "unknown_error"
			}
			return nil, &APIError{Method: method, Code: code, Needed: env.Needed, Provided: env.Provided}
		}
		if out != nil {
			if err := json.Unmarshal(body, out); err != nil {
				return nil, fmt.Errorf("slack %s: decode the response", method)
			}
		}
		return header, nil
	}
}

func (c *Client) post(ctx context.Context, method string, token bearer, args url.Values) (int, http.Header, []byte, error) {
	tok, err := token(ctx)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("slack %s: token: %w", method, err)
	}
	base := c.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+method, strings.NewReader(args.Encode()))
	if err != nil {
		return 0, nil, nil, fmt.Errorf("slack %s: build the request", method)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	hc := c.HTTP
	if hc == nil {
		hc = defaultHTTP
	}
	resp, err := hc.Do(req)
	if err != nil {
		c.logRequest(method, 0)
		return 0, nil, nil, fmt.Errorf("slack %s: %w", method, err)
	}
	defer resp.Body.Close()
	c.logRequest(method, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("slack %s: read the response: %w", method, err)
	}
	return resp.StatusCode, resp.Header, body, nil
}

func (c *Client) logRequest(method string, status int) {
	if c.RequestLog != nil {
		c.RequestLog(method, status)
	}
}

func (c *Client) limiter() *Limiter {
	if c.Limits != nil {
		return c.Limits
	}
	c.once.Do(func() { c.fallback = &Limiter{} })
	return c.fallback
}

func retryAfter(h http.Header) time.Duration {
	secs, err := strconv.Atoi(strings.TrimSpace(h.Get("Retry-After")))
	if err != nil || secs < 0 {
		return defaultRetryAfter
	}
	return time.Duration(secs) * time.Second
}
