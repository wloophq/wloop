package outbox

import (
	"context"
	"fmt"
	"slices"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// CloseSilently applies the effects of draft id, which a person discarded, without posting it: the draft
// keeps its state, its reason becomes draft.ReasonClosedSilently, then the Applier runs its effects. A
// failed effect is retried by ApplyPending. Refused (ErrInvalid) unless the draft was discarded by a person,
// declares effects and every one of them applies without a post.
func (s *Service) CloseSilently(ctx context.Context, id draft.ID) (draft.Draft, error) {
	d, err := s.Store.Get(ctx, id)
	if err != nil {
		return draft.Draft{}, err
	}
	if err := s.closableSilently(d); err != nil {
		return draft.Draft{}, err
	}
	d.Reason = draft.ReasonClosedSilently
	if d, err = s.save(ctx, d, draft.Dropped); err != nil {
		return draft.Draft{}, err
	}
	if s.Applier == nil {
		return d, nil
	}
	if err := s.Applier.Apply(ctx, d); err != nil {
		return d, err
	}
	return s.Store.Get(ctx, id)
}

// closableSilently says why d cannot close without its message, nil when it can.
func (s *Service) closableSilently(d draft.Draft) error {
	switch {
	case d.State != draft.Dropped || d.Reason != reasonUser:
		return fmt.Errorf("%w: draft %d is %s (%s), not discarded by a person", ErrInvalid, d.ID, d.State, d.Reason)
	case len(d.Effects) == 0:
		return fmt.Errorf("%w: draft %d applies nothing without its message", ErrInvalid, d.ID)
	case s.Silent == nil || slices.ContainsFunc(d.Effects, func(e string) bool { return !s.Silent(e) }):
		return fmt.Errorf("%w: an effect of draft %d needs its message", ErrInvalid, d.ID)
	}
	return nil
}

// EffectRunner is an optional capability of an Applier: it runs the effects d declares with no record of them.
type EffectRunner interface {
	Run(ctx context.Context, d draft.Draft) error
}

// SilentlyApplicable says why effects cannot apply on a unit of provider p with no draft, nil when they can:
// it names effects, every one of them applies without a post, and the Applier implements each for p.
func (s *Service) SilentlyApplicable(p unit.ProviderID, effects []string) error {
	switch {
	case len(effects) == 0:
		return fmt.Errorf("%w: no effect to apply", ErrInvalid)
	case s.Silent == nil || slices.ContainsFunc(effects, func(e string) bool { return !s.Silent(e) }):
		return fmt.Errorf("%w: an effect of %v needs a draft's post", ErrInvalid, effects)
	}
	if _, ok := s.Applier.(EffectRunner); !ok {
		return fmt.Errorf("%w: the outbox applies no effect without a draft", ErrInvalid)
	}
	if impl, ok := s.Applier.(Implementer); ok {
		for _, e := range effects {
			if !impl.Implements(p, e) {
				return fmt.Errorf("%w: %s has no implementation for %s", ErrInvalid, e, p)
			}
		}
	}
	return nil
}

// ApplySilently applies effects on u with no draft and nothing posted, as a workflow step does; refused
// (ErrInvalid) unless SilentlyApplicable allows them.
func (s *Service) ApplySilently(ctx context.Context, u unit.Unit, effects []string) error {
	if err := s.SilentlyApplicable(u.Provider, effects); err != nil {
		return err
	}
	return s.Applier.(EffectRunner).Run(ctx, draft.Draft{Unit: u, Effects: effects, Evidence: draft.Evidence{At: s.Clock.Now()}})
}
