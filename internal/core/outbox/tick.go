package outbox

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/domain/draft"
)

// Tick approves every proposed delay draft whose Deadline passed, through the
// same gate as Approve; a draft the gate refuses stays proposed.
func (s *Service) Tick(ctx context.Context) (approved int, err error) {
	pending, err := s.windowDrafts(ctx)
	if err != nil {
		return 0, err
	}
	now := s.Clock.Now()
	var errs []error
	for _, d := range pending {
		if d.Deadline.After(now) {
			continue
		}
		_, ok, err := s.approveByPolicy(ctx, d)
		switch {
		case errors.Is(err, ErrConflict):
			s.logf("draft %d changed before its window closed", d.ID)
		case err != nil:
			errs = append(errs, err)
		case ok:
			approved++
		}
	}
	return approved, errors.Join(errs...)
}

// NextDeadline is the earliest Deadline of a proposed delay draft; false when there is none.
func (s *Service) NextDeadline(ctx context.Context) (time.Time, bool, error) {
	pending, err := s.windowDrafts(ctx)
	if err != nil || len(pending) == 0 {
		return time.Time{}, false, err
	}
	next := pending[0].Deadline
	for _, d := range pending[1:] {
		if d.Deadline.Before(next) {
			next = d.Deadline
		}
	}
	return next, true, nil
}

// windowDrafts are the proposed delay drafts whose window may end: not one
// of a workflow that failed to load, nor one stored with another delivery
// than its publisher posts it as, which the comment floor never decided.
func (s *Service) windowDrafts(ctx context.Context) ([]draft.Draft, error) {
	proposed, err := s.Store.List(ctx, Filter{States: []draft.State{draft.Proposed}})
	if err != nil {
		return nil, err
	}
	var broken []string
	if s.Broken != nil {
		broken = s.Broken()
	}
	out := proposed[:0]
	for _, d := range proposed {
		switch {
		case d.Approval.Mode != draft.Delay || d.Deadline.IsZero():
		case d.Origin.Workflow != "" && slices.Contains(broken, d.Origin.Workflow):
		case s.delivered(d).Delivery != d.Delivery:
		default:
			out = append(out, d)
		}
	}
	return out, nil
}

// approveByPolicy moves proposed d to approved with Outcome auto when the gate
// lets it; ok is false, with the reason logged, when the gate refuses.
func (s *Service) approveByPolicy(ctx context.Context, d draft.Draft) (draft.Draft, bool, error) {
	if err := s.publishable(ctx, d); err != nil {
		s.logf("draft %d stays proposed: %v", d.ID, err)
		return d, false, nil
	}
	saved, err := s.markAuto(ctx, d)
	if err != nil {
		return d, false, err
	}
	return saved, true, nil
}

func (s *Service) markAuto(ctx context.Context, d draft.Draft) (draft.Draft, error) {
	d.State, d.Would.Outcome = draft.Approved, draft.OutcomeReleased
	return s.save(ctx, d, draft.Proposed)
}
