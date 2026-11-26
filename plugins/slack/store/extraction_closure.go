package store

import (
	"context"
	"time"

	"github.com/wloophq/wloop/plugins/slack/unit"
)

const markClosedSQL = `INSERT INTO extraction_cursors (provider, unit_key, closure_marked, closed_ts, at) VALUES (?, ?, 1, ?, ?)
	ON CONFLICT (provider, unit_key) DO UPDATE SET closure_marked = 1, closed_ts = excluded.closed_ts`

// MarkClosed records u's unit.closed at at, which no extraction pass
// clears, with newest, the ts of the newest message u had then.
func (s *ExtractionStore) MarkClosed(ctx context.Context, u unit.Unit, at time.Time, newest string) error {
	_, err := s.db.Write(ctx, func(tx *Tx) error {
		tx.Touch(u)
		_, err := tx.ExecContext(ctx, markClosedSQL, string(u.Provider), u.Key, newest, formatTime(at))
		return err
	})
	return err
}
