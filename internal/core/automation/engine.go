// Package automation is the engine that plans and runs workflows over units.
package automation

import (
	"context"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

type Engine struct {
	Workflows func() []workflow.Workflow
	// Broken names the workflows that failed to load; their proposed drafts
	// stay until they load again or are gone. Nil: none failed.
	Broken   func() []string
	Units    Units
	Events   Events
	Steps    Steps
	Ledger   Ledger
	Guidance Guidance
	Changes  port.Changes // the wake (TopicSourceEvents for event workflows) and the revalidation under the lock
	Clock    port.Clock
	Codecs   *unit.Registry
	Pause    func(ctx context.Context) (Pause, error)
	Config   func() workflow.Facts
	State    StateLoader
	Marks    Marks // nil reads every unit as having no marks
	// Withdrawals are the drafts the workflows proposed; nil withdraws none.
	Withdrawals Withdrawals
	// PluginEnabled says whether a provider's plugin is on; nil: every one
	// is. No draft of a disabled plugin's unit is withdrawn.
	PluginEnabled func(unit.ProviderID) bool
	Reachable     func(ctx context.Context) error
	RetryBase     time.Duration
	Pub           port.Publisher
	Log           func(format string, args ...any)

	mu     sync.Mutex
	due    time.Time
	seen   port.Generation
	events heldEvents
}

type Plan struct {
	Items  []workflow.Item
	Errors []string
	Gen    port.Generation // the change log generation the evaluation read at
}

// Revalidate are the topics whose notes after a plan's generation drop the
// plan's run items on the noted units: the base sweep's set.
const Revalidate = port.TopicPRs | port.TopicSourceEvents | port.TopicTurns | port.TopicOverrides

// DefaultRetryBase is the first retry delay of a failed step when RetryBase is zero.
const DefaultRetryBase = time.Minute

// Due is the earliest instant after the last sweep at which a decision
// changes with no input changing: a pending deadline, a retry time, the
// next schedule firing, or a facts provider's own deadline. Zero when none.
func (e *Engine) Due() time.Time {
	e.mu.Lock()
	due := e.due
	e.mu.Unlock()
	if d, ok := e.Units.(Dueer); ok {
		due = earliest(due, d.Due())
	}
	return due
}

// Seen is the workspace snapshot generation the last sweep read.
func (e *Engine) Seen() port.Generation {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.seen
}

func (e *Engine) now() time.Time { return e.Clock.Now() }

func (e *Engine) logf(format string, args ...any) {
	if e.Log != nil {
		e.Log(format, args...)
	}
}

func (e *Engine) retryBase() time.Duration {
	if e.RetryBase > 0 {
		return e.RetryBase
	}
	return DefaultRetryBase
}

func (e *Engine) workflows() []workflow.Workflow {
	if e.Workflows == nil {
		return nil
	}
	return e.Workflows()
}

func (e *Engine) gen() port.Generation {
	if e.Changes == nil {
		return 0
	}
	return e.Changes.Gen()
}

func (e *Engine) publish(evs []port.Event) {
	if e.Pub != nil && len(evs) > 0 {
		e.Pub.Publish(evs...)
	}
}

// earliest is the earlier non-zero time of a and b.
func earliest(a, b time.Time) time.Time {
	switch {
	case a.IsZero():
		return b
	case b.IsZero() || a.Before(b):
		return a
	}
	return b
}
