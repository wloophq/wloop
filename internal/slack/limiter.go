package slack

import (
	"context"
	"sync"
	"time"
)

// Budgets per minute are the floors of Slack's documented tiers ("Tier 3: 50+ per minute"),
// so they sit below the real limit.
const (
	tier1   = 1
	tier2   = 20
	tier3   = 50
	tier4   = 100
	special = 60
)

var defaultPerMinute = map[string]int{
	"apps.connections.open": tier1,
	"search.messages":       tier2,
	"conversations.list":    tier2,
	"users.list":            tier2,
	"reactions.remove":      tier2,
	"conversations.history": tier3,
	"conversations.replies": tier3,
	"conversations.info":    tier3,
	"conversations.open":    tier3,
	"users.lookupByEmail":   tier3,
	"reactions.get":         tier3,
	"reactions.add":         tier3,
	"auth.revoke":           tier3,
	"users.info":            tier4,
	"oauth.v2.access":       tier4,
	"auth.test":             tier4,
	"chat.getPermalink":     tier4,
	"chat.postMessage":      special,
}

const unknownMethodPerMinute = tier3

// Limiter paces each method with a token bucket of one minute's budget and
// holds it for the Retry-After window of an HTTP 429. The zero value uses the default tiers.
type Limiter struct {
	// PerMinute overrides the budget of the methods it names.
	PerMinute map[string]int

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens     float64
	perMinute  float64
	last       time.Time
	blockedTil time.Time
}

// Low reports whether method is inside a Retry-After window or its tier budget for the minute is spent.
func (l *Limiter) Low(method string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b := l.bucket(method, now)
	return now.Before(b.blockedTil) || b.tokens < 1
}

// Wait blocks until method may be called, then spends one unit of its budget.
func (l *Limiter) Wait(ctx context.Context, method string) error {
	for {
		l.mu.Lock()
		now := time.Now()
		b := l.bucket(method, now)
		var delay time.Duration
		switch {
		case now.Before(b.blockedTil):
			delay = b.blockedTil.Sub(now)
		case b.tokens < 1:
			delay = time.Duration((1 - b.tokens) / b.perMinute * float64(time.Minute))
		default:
			b.tokens--
			l.mu.Unlock()
			return nil
		}
		l.mu.Unlock()
		if err := sleep(ctx, delay); err != nil {
			return err
		}
	}
}

func (l *Limiter) hold(method string, d time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b := l.bucket(method, now)
	if until := now.Add(d); until.After(b.blockedTil) {
		b.blockedTil = until
	}
}

func (l *Limiter) bucket(method string, now time.Time) *bucket {
	if l.buckets == nil {
		l.buckets = map[string]*bucket{}
	}
	b := l.buckets[method]
	if b == nil {
		per := float64(l.perMinute(method))
		b = &bucket{tokens: per, perMinute: per, last: now}
		l.buckets[method] = b
		return b
	}
	if elapsed := now.Sub(b.last); elapsed > 0 {
		b.tokens = min(b.perMinute, b.tokens+elapsed.Minutes()*b.perMinute)
		b.last = now
	}
	return b
}

func (l *Limiter) perMinute(method string) int {
	if n, ok := l.PerMinute[method]; ok && n > 0 {
		return n
	}
	if n, ok := defaultPerMinute[method]; ok {
		return n
	}
	return unknownMethodPerMinute
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
