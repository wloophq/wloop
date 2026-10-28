package tasks

import (
	"context"
	"errors"
	"time"

	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Nudged records a nudge sent at at on the live waiting entries ids names, or, with no ids, on every live
// waiting entry of u. It reads them under the service's lock, so it never writes back a copy older than a
// concurrent Sync's, and it never moves LastNudge back.
func (s *Service) Nudged(ctx context.Context, ids []task.ID, u unit.Unit, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var es []task.Entry
	if len(ids) == 0 {
		got, err := s.d.Store.EntriesOf(ctx, u)
		if err != nil {
			return err
		}
		es = got
	}
	for _, id := range ids {
		e, err := s.d.Store.Entry(ctx, id)
		if errors.Is(err, ErrNoEntry) {
			continue
		}
		if err != nil {
			return err
		}
		es = append(es, e)
	}
	now := s.now()
	var cs []task.Change
	for _, e := range es {
		if e.Waiting == nil || !e.Live() || !at.After(e.Waiting.LastNudge) {
			continue
		}
		w := *e.Waiting
		w.LastNudge = at
		e.Waiting, e.Updated = &w, now
		cs = append(cs, task.Change{Op: task.OpUpdate, Entry: e, Why: "nudged"})
	}
	if len(cs) == 0 {
		return nil
	}
	return s.d.Store.Apply(ctx, cs)
}
