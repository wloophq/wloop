package outbox

import (
	"context"
	"fmt"
	"slices"

	"github.com/wloophq/wloop/internal/domain/draft"
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
