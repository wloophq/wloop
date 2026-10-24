package turns

import (
	"context"

	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// NewestAnswer is the newest answer the user gave to a turn of the unit the
// handle's session owns, as the agent reads it, but one still queued; false
// when there is none.
func (s *Service) NewestAnswer(ctx context.Context, u unit.Unit, handle string) (string, bool, error) {
	b, err := s.book(ctx, u)
	if err != nil {
		return "", false, err
	}
	for i := len(b.Answers) - 1; i >= 0; i-- {
		a := b.Answers[i]
		if a.Source == turn.Terminal || a.Route == turn.Queued {
			continue
		}
		if t, ok := bySeq(&b, a.TurnSeq); ok && (owns(t, handle) || (handle != "" && a.Session == handle)) {
			return b.Said(a), true, nil
		}
	}
	return "", false, nil
}
