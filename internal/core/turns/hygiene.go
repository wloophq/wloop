package turns

import (
	"context"
	"fmt"
	"slices"
	"strconv"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// DismissTurn closes the unit's open or stopped turn seq for reason and
// refuses its queued and pending answers, so none is typed into it later.
func (s *Service) DismissTurn(ctx context.Context, u unit.Unit, seq int64, reason string) error {
	s.mu.Lock()
	b, err := s.book(ctx, u)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	t, ok := bySeq(&b, seq)
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("%s has no turn %d", u.Label(), seq)
	}
	if !t.State.Open() && !t.State.Stopped() {
		s.mu.Unlock()
		return Refusal{fmt.Sprintf("turn %s is %s", t.ID, t.State)}
	}
	t.State, t.EndedAt = turn.Dismissed, s.now()
	evs := append([]port.Event{s.event(u, KindTurnDismissedByRule, strconv.FormatInt(seq, 10)+":"+reason, reason)}, s.refuseQueued(&b, seq, false)...)
	for i := range b.Answers {
		if a := &b.Answers[i]; a.TurnSeq == seq && a.Route == turn.Pending {
			a.Route = turn.Refused
			evs = append(evs, s.event(u, KindAnswerRefused, strconv.FormatInt(a.ID, 10), "its turn was dismissed"))
		}
	}
	_, err = s.Store.Save(ctx, b, evs...)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	s.publish(evs...)
	s.changed(ctx, u)
	return nil
}

// RefuseAnswer refuses the unit's pending or queued answer id for reason; an
// answer already refused is left as it is.
func (s *Service) RefuseAnswer(ctx context.Context, u unit.Unit, id int64, reason string) error {
	s.mu.Lock()
	b, err := s.book(ctx, u)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	i := slices.IndexFunc(b.Answers, func(a turn.Answer) bool { return a.ID == id })
	if i < 0 {
		s.mu.Unlock()
		return fmt.Errorf("%s has no answer %d", u.Label(), id)
	}
	switch a := &b.Answers[i]; a.Route {
	case turn.Refused:
		s.mu.Unlock()
		return nil
	case turn.Pending, turn.Queued:
		a.Route = turn.Refused
	default:
		s.mu.Unlock()
		return Refusal{fmt.Sprintf("answer %d is %s", id, a.Route)}
	}
	ev := s.event(u, KindAnswerRefused, strconv.FormatInt(id, 10), reason)
	_, err = s.Store.Save(ctx, b, ev)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	s.publish(ev)
	s.changed(ctx, u)
	return nil
}
