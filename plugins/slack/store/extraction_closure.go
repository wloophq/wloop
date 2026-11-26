package store

import (
	"context"
	"time"

	"github.com/wloophq/wloop/plugins/slack/unit"
)

const markClosedSQL = `INSERT INTO extraction_cursors (provider, unit_key, closure_marked, closed_at, at) VALUES (?, ?, 1, ?, ?)
	ON CONFLICT (provider, unit_key) DO UPDATE SET closure_marked = 1, closed_at = excluded.closed_at`

// MarkClosed records u's unit.closed at at, which no extraction pass
// clears.
func (s *ExtractionStore) MarkClosed(ctx context.Context, u unit.Unit, at time.Time) error {
	_, err := s.db.Write(ctx, func(tx *Tx) error {
		tx.Touch(u)
		_, err := tx.ExecContext(ctx, markClosedSQL, string(u.Provider), u.Key, formatTime(at), formatTime(at))
		return err
	})
	return err
}
