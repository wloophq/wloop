// Package automationtest holds in-memory fakes of the engine's ports and
// the contract suites every implementation runs.
package automationtest

import (
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// Option configures a fake.
type Option func(*options)

type options struct {
	strict testing.TB
	trace  func(call string)
	notes  Noter
}

// Strict makes the fake fail t on any write.
func Strict(t testing.TB) Option { return func(o *options) { o.strict = t } }

// Trace calls fn with the name of every call the fake receives
// ("units.list", "steps.run", "ledger.save", ...), before it runs.
func Trace(fn func(call string)) Option { return func(o *options) { o.trace = fn } }

func build(opts []Option) options {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

func (o options) read(call string) {
	if o.trace != nil {
		o.trace(call)
	}
}

func (o options) write(call string) {
	o.read(call)
	if o.strict != nil {
		o.strict.Helper()
		o.strict.Errorf("%s writes under a strict fake", call)
	}
}

type matchFunc struct {
	fn    func(workflow.Vars) bool
	reads []string
}

func (m matchFunc) Match(v workflow.Vars) (bool, error) { return m.fn(v), nil }
func (m matchFunc) Reads() []string                     { return m.reads }

// MatchFunc is a predicate over fn that names reads as the facts it reads.
func MatchFunc(fn func(workflow.Vars) bool, reads ...string) workflow.Predicate {
	return matchFunc{fn: fn, reads: reads}
}

func compareUnits(a, b unit.Unit) int {
	switch {
	case a.Less(b):
		return -1
	case b.Less(a):
		return 1
	}
	return 0
}

// Clock is a settable clock.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

func NewClock(t time.Time) *Clock { return &Clock{now: t} }

func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func (o *options) seal(t testing.TB) { o.strict = t }
