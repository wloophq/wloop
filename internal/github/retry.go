package github

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	"net"
	"net/http"
	"strings"
	"time"
)

// Retry bounds the retries of one call and the breaker every call shares.
type Retry struct {
	Attempts   int           // tries in all; 3
	Base, Max  time.Duration // full-jitter backoff bounds; 1 s, 30 s
	BreakAfter int           // consecutive network failures that open the breaker; 3
	BreakFor   time.Duration // how long the open breaker fails fast; 60 s
}

func (r Retry) withDefaults() Retry {
	if r.Attempts <= 0 {
		r.Attempts = 3
	}
	if r.Base <= 0 {
		r.Base = time.Second
	}
	if r.Max <= 0 {
		r.Max = 30 * time.Second
	}
	if r.BreakAfter <= 0 {
		r.BreakAfter = 3
	}
	if r.BreakFor <= 0 {
		r.BreakFor = time.Minute
	}
	return r
}

// RateLimitedError is a resource GitHub asked to leave alone until RetryAt.
// Secondary is a secondary (abuse) limit; otherwise the bucket ran out.
type RateLimitedError struct {
	Resource  dgh.Resource
	RetryAt   time.Time
	Secondary bool
}

func (e *RateLimitedError) Error() string {
	kind := "rate limit"
	if e.Secondary {
		kind = "secondary rate limit"
	}
	return fmt.Sprintf("GitHub %s on %s until %s", kind, e.Resource, e.RetryAt.Format(time.RFC3339))
}

func (e *RateLimitedError) Is(target error) bool { return target == ErrRateLimited }

var errBreaker = errors.New("breaker open")

const secondaryPause = time.Minute

type pause struct {
	until     time.Time
	secondary bool
}

// transportError is an attempt that got no complete response.
type transportError struct {
	err          error
	timeout      bool
	timeoutAfter time.Duration
	connected    bool
}

func (e *transportError) Error() string {
	if e.timeout {
		return fmt.Sprintf("attempt timed out after %s: %v", e.timeoutAfter, e.err)
	}
	return e.err.Error()
}

// Unwrap hides the cause of an attempt timeout, so the attempt's
// context.DeadlineExceeded never reads as the caller's.
func (e *transportError) Unwrap() error {
	if e.timeout {
		return errTimedOut
	}
	return e.err
}

// unsent says the request never left: no connection was handed to it and
// the failure is the dial's.
func (e *transportError) unsent() bool {
	if e.connected {
		return false
	}
	var dns *net.DNSError
	if errors.As(e.err, &dns) {
		return true
	}
	var op *net.OpError
	return errors.As(e.err, &op) && (op.Op == "dial" || op.Op == "proxyconnect")
}

type exchange struct {
	op       string // "graphql" or "rest"
	res      dgh.Resource
	build    func(context.Context) (*http.Request, error)
	maxBytes int64
	attempts int
	mutation bool
}

// do sends x through an in-flight slot, then the pauses, the budget and the
// breaker, retrying a gateway status, a network failure or an attempt
// timeout up to x.attempts with full-jitter backoff. A Mutation is retried
// only when its request never left. An empty GraphQL answer is retried once
// on top of x.attempts and leaves the breaker as it was.
func (c *Client) do(ctx context.Context, x exchange) (Response, error) {
	p := dgh.PriorityOf(ctx)
	retriedEmpty := false
	for n := 0; ; n++ {
		release, err := c.slots.acquire(ctx, p)
		if err != nil {
			return Response{}, err
		}
		probe, err := c.admit(x.res, p)
		if err != nil {
			release()
			return Response{}, err
		}
		resp, err := c.send(ctx, x)
		c.observe(x.res, resp.Limit)
		var te *transportError
		network := errors.As(err, &te) && ctx.Err() == nil
		empty := err == nil && x.op == "graphql" && emptyAnswer(resp)
		c.settle(err, network, empty, probe)
		var rl *RateLimitedError
		if err == nil {
			rl = c.rateLimited(x.res, resp)
		}
		release()
		if rl != nil {
			return resp, rl
		}
		retry := n+1 < x.attempts && ctx.Err() == nil &&
			(network || err == nil && gatewayStatus(resp.Status))
		if retry && x.mutation {
			retry = network && te.unsent()
		}
		if empty {
			retry = !retriedEmpty && ctx.Err() == nil
			retriedEmpty = true
		}
		if !retry {
			if network {
				return resp, fmt.Errorf("%w: %w", ErrUnavailable, err)
			}
			return resp, err
		}
		if err := c.sleep(ctx, c.backoff(n)); err != nil {
			return resp, err
		}
	}
}

// observe keeps the bucket a response's headers reported.
func (c *Client) observe(res dgh.Resource, l dgh.Limit) {
	if l.Resource == "" {
		l.Resource = res
	}
	c.budget.observe(l, c.now())
}

// backoff is the full-jitter pause before retry n+1: [0, min(Max, Base*2^n)).
func (c *Client) backoff(n int) time.Duration {
	ceiling := c.retry.Max
	if n < 32 && c.retry.Base<<n > 0 && c.retry.Base<<n < ceiling {
		ceiling = c.retry.Base << n
	}
	return time.Duration(c.jitter(int64(ceiling)))
}

// admit refuses a call on a paused resource, below the floor of its
// priority or behind an open breaker; once BreakFor passed, it lets one
// probe through and says so.
func (c *Client) admit(res dgh.Resource, prio dgh.Priority) (probe bool, err error) {
	c.gate.Lock()
	defer c.gate.Unlock()
	now := c.now()
	if p, ok := c.paused[res]; ok {
		if now.Before(p.until) {
			return false, &RateLimitedError{Resource: res, RetryAt: p.until, Secondary: p.secondary}
		}
		delete(c.paused, res)
	}
	if be := c.budget.refusal(res, prio, now); be != nil {
		return false, be
	}
	if c.fails < c.retry.BreakAfter {
		return false, nil
	}
	if now.Before(c.openUntil) || c.probing {
		return false, fmt.Errorf("%w: %w after %d network failures, until %s", ErrUnavailable, errBreaker, c.fails, c.openUntil.Format(time.RFC3339))
	}
	c.probing = true
	return true, nil
}

// settle counts a network failure toward the breaker; any answer but an
// empty one closes it.
func (c *Client) settle(err error, network, empty, probe bool) {
	c.gate.Lock()
	defer c.gate.Unlock()
	switch {
	case empty:
	case network:
		c.fails++
		if c.fails >= c.retry.BreakAfter {
			c.openUntil = c.now().Add(c.retry.BreakFor)
		}
	case err == nil:
		c.fails = 0
	}
	if probe {
		c.probing = false
	}
}

// rateLimited pauses the resource a 403 or 429 names as rate limited and
// says until when; nil when the status is not a rate limit.
func (c *Client) rateLimited(res dgh.Resource, resp Response) *RateLimitedError {
	if resp.Status != http.StatusForbidden && resp.Status != http.StatusTooManyRequests {
		return nil
	}
	if resp.Limit.Resource != "" {
		res = resp.Limit.Resource
	}
	now := c.now()
	switch {
	case resp.retryAfter > 0:
		return c.pause(res, now.Add(resp.retryAfter), true)
	case resp.Status == http.StatusTooManyRequests || namesSecondaryLimit(resp.Body):
		return c.pause(res, now.Add(secondaryPause), true)
	case resp.Limit.Limit > 0 && resp.Limit.Remaining == 0 && resp.Limit.ResetAt.After(now):
		return c.pause(res, resp.Limit.ResetAt, false)
	}
	return nil
}

// emptyAnswer is a 200 GitHub sent without a body, which it does now and
// then for a search it answers on the next try.
func emptyAnswer(resp Response) bool {
	return resp.Status == http.StatusOK && len(bytes.TrimSpace(resp.Body)) == 0
}

func namesSecondaryLimit(body []byte) bool {
	return bytes.Contains(bytes.ToLower(body), []byte("secondary rate limit"))
}

func (c *Client) pause(res dgh.Resource, until time.Time, secondary bool) *RateLimitedError {
	c.gate.Lock()
	defer c.gate.Unlock()
	p, ok := c.paused[res]
	if !ok || until.After(p.until) {
		p = pause{until: until, secondary: secondary}
		c.paused[res] = p
	}
	return &RateLimitedError{Resource: res, RetryAt: p.until, Secondary: p.secondary}
}

// queryError is the error of a GraphQL response that failed as a whole. A
// RATE_LIMITED error pauses graphql like a 403 would.
func (c *Client) queryError(resp Response, e gqlError) error {
	if e.Type == "RATE_LIMITED" {
		if l := resp.Limit; l.Limit > 0 && l.Remaining == 0 && l.ResetAt.After(c.now()) {
			return c.pause(dgh.ResourceGraphQL, l.ResetAt, false)
		}
		return c.pause(dgh.ResourceGraphQL, c.now().Add(secondaryPause), true)
	}
	if m := strings.ToLower(e.Message); strings.Contains(m, "timeout") || strings.Contains(m, "timed out") {
		return fmt.Errorf("graphql: %s: %w", e.Message, errTimedOut)
	}
	return fmt.Errorf("graphql: %s", e.Message)
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
