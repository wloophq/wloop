package outbox

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// AppliedStore keeps when a sent draft's effects were applied.
type AppliedStore interface {
	// Unapplied are the sent drafts that declare effects and whose
	// EffectsAppliedAt is zero, oldest first.
	Unapplied(ctx context.Context) ([]draft.Draft, error)
	// MarkApplied stores at as the draft's EffectsAppliedAt.
	MarkApplied(ctx context.Context, id draft.ID, at time.Time) error
}

// EffectLookup is the implementation of effect name for drafts of provider
// p; false when there is none.
type EffectLookup func(p unit.ProviderID, name string) (Effect, bool)

// EffectApplier is the Applier over a store and the effect implementations.
type EffectApplier struct {
	Store   AppliedStore
	Effects EffectLookup
	Clock   port.Clock
}

var _ Implementer = EffectApplier{}

// Implementer is an Applier that says whether it has effect name for the
// drafts of provider p, so Propose refuses a draft it could never apply.
type Implementer interface {
	Applier
	Implements(p unit.ProviderID, name string) bool
}

func (a EffectApplier) Implements(p unit.ProviderID, name string) bool {
	_, ok := a.Effects(p, name)
	return ok
}

// ErrUnknownEffect is an effect a draft declares that has no implementation
// for its provider.
var ErrUnknownEffect = errors.New("no implementation of the effect")

// Apply runs every effect d declares, in order, then marks d applied; a
// draft not sent, or already applied, is left as it is. When an effect
// fails nothing is marked, so the next Pending pass runs them all again:
// each effect is idempotent.
func (a EffectApplier) Apply(ctx context.Context, d draft.Draft) error {
	if d.State != draft.Sent || !d.EffectsAppliedAt.IsZero() {
		return nil
	}
	for _, name := range d.Effects {
		fx, ok := a.Effects(d.Unit.Provider, name)
		if !ok {
			return fmt.Errorf("%s on %s: %w", name, d.Unit.Provider, ErrUnknownEffect)
		}
		if err := fx(ctx, d); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return a.Store.MarkApplied(ctx, d.ID, a.Clock.Now())
}

func (a EffectApplier) Pending(ctx context.Context) ([]draft.Draft, error) {
	return a.Store.Unapplied(ctx)
}

// ApplyPending applies the effects of every sent draft not applied yet: a
// draft sent before a restart, or whose effects failed.
func (s *Service) ApplyPending(ctx context.Context) error {
	if s.Applier == nil {
		return nil
	}
	pending, err := s.Applier.Pending(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, d := range pending {
		if err := s.Applier.Apply(ctx, d); err != nil {
			s.logf("draft %d: its effects were not applied; the next pass retries", d.ID)
			errs = append(errs, fmt.Errorf("effects of draft %d: %w", d.ID, err))
		}
	}
	return errors.Join(errs...)
}
