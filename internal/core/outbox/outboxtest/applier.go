// Package outboxtest holds fakes of the Outbox's ports.
package outboxtest

import (
	"context"
	"slices"
	"sync"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
)

// Applier records every draft it applies, once per draft: applying a draft
// again is a no-op, as the real applier's EffectsAppliedAt makes it.
type Applier struct {
	mu      sync.Mutex
	applied []draft.Draft
	pending []draft.Draft
	// Err, when set, fails every Apply.
	Err error
}

var _ outbox.Applier = (*Applier)(nil)

func (a *Applier) Apply(_ context.Context, d draft.Draft) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.Err != nil {
		return a.Err
	}
	if slices.ContainsFunc(a.applied, func(x draft.Draft) bool { return x.ID == d.ID }) {
		return nil
	}
	a.applied = append(a.applied, d)
	a.pending = slices.DeleteFunc(a.pending, func(x draft.Draft) bool { return x.ID == d.ID })
	return nil
}

// Pending is what SetPending last set, less the drafts applied since.
func (a *Applier) Pending(context.Context) ([]draft.Draft, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.pending), nil
}

// SetPending sets the sent drafts Pending lists.
func (a *Applier) SetPending(ds ...draft.Draft) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pending = slices.Clone(ds)
}

// Applied are the drafts applied, in order.
func (a *Applier) Applied() []draft.Draft {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.applied)
}
