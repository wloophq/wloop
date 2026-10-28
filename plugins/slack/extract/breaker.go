package extract

import (
	"fmt"
	"sync"
	"time"
)

const (
	// BreakerAfter consecutive failed calls open the breaker for BreakerCool; the next call is its trial.
	BreakerAfter = 3
	BreakerCool  = 15 * time.Minute
)

// Breaker events, logged as they happen.
const (
	EventBreakerOpened      = "extract_breaker_opened"
	EventBreakerClosed      = "extract_breaker_closed"
	EventBreakerTrialFailed = "extract_breaker_trial_failed"
)

// breaker lives in memory: a daemon starts closed.
type breaker struct {
	mu      sync.Mutex
	fails   int
	open    bool
	until   time.Time
	lastErr string
}

// allow says whether a call may run: closed, or open past its cool for the trial.
func (b *breaker) allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return !b.open || !now.Before(b.until)
}

func (b *breaker) failed(now time.Time, err error) (event string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lastErr = err.Error()
	if b.open {
		b.until = now.Add(BreakerCool)
		return EventBreakerTrialFailed
	}
	b.fails++
	if b.fails < BreakerAfter {
		return ""
	}
	b.open, b.until = true, now.Add(BreakerCool)
	return EventBreakerOpened
}

func (b *breaker) succeeded() (event string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	was := b.open
	b.fails, b.open, b.lastErr = 0, false, ""
	if was {
		return EventBreakerClosed
	}
	return ""
}

func (b *breaker) warnings() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.open {
		return nil
	}
	return []string{fmt.Sprintf("slack extraction: the breaker is open after %d failed calls (last: %s); the next trial is at %s",
		BreakerAfter, b.lastErr, b.until.Local().Format("15:04"))}
}
