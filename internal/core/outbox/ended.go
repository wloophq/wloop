package outbox

import (
	"context"
	"errors"
	"slices"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
)

// Ended withdraws the held drafts of task id, a verb ended, as draft.WithdrawEnded, but those of a purpose keep
// names; it returns the drafts it withdrew.
func (s *Service) Ended(ctx context.Context, id task.ID, keep []string) ([]draft.Draft, error) {
	if id == "" {
		return nil, nil
	}
	held, err := s.Store.List(ctx, Filter{States: []draft.State{draft.Held}, Task: string(id)})
	if err != nil {
		return nil, err
	}
	var out []draft.Draft
	var errs []error
	for _, d := range held {
		if slices.Contains(keep, d.Purpose) {
			continue
		}
		got, err := s.Withdraw(ctx, d.ID, draft.WithdrawEnded)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if got.State == draft.Dropped {
			out = append(out, got)
		}
	}
	return out, errors.Join(errs...)
}

// Adopt gives each held draft of no task the task its open gate waits on, and withdraws, as draft.WithdrawEnded,
// one whose task ended says has ended; a draft whose gate is on no task stays as it is.
func (s *Service) Adopt(ctx context.Context, ended func(ctx context.Context, id task.ID) (bool, error)) (adopted, withdrawn int, err error) {
	held, err := s.Store.List(ctx, Filter{States: []draft.State{draft.Held}})
	if err != nil {
		return 0, 0, err
	}
	var errs []error
	for _, d := range held {
		if d.Task != "" {
			continue
		}
		g, ok, err := s.GateOf(ctx, d)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !ok || g.Status != gate.Open || g.Task == "" {
			continue
		}
		gone, err := ended(ctx, g.Task)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if d, err = s.adoptGate(ctx, d, g); err != nil {
			errs = append(errs, err)
			continue
		}
		if !gone {
			adopted++
			continue
		}
		if _, err := s.Withdraw(ctx, d.ID, draft.WithdrawEnded); err != nil {
			errs = append(errs, err)
			continue
		}
		withdrawn++
	}
	return adopted, withdrawn, errors.Join(errs...)
}

// adoptGate gives held draft d the task its gate g was placed on when d names none, so the task carries it.
func (s *Service) adoptGate(ctx context.Context, d draft.Draft, g gate.Gate) (draft.Draft, error) {
	if d.Task != "" || g.Task == "" {
		return d, nil
	}
	cur, err := s.Store.Get(ctx, d.ID)
	if err != nil || cur.State != draft.Held {
		return cur, err
	}
	cur.Task = string(g.Task)
	got, err := s.save(ctx, cur, draft.Held)
	if errors.Is(err, ErrConflict) {
		return s.Store.Get(ctx, d.ID)
	}
	return got, err
}
