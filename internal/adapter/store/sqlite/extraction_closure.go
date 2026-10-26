package sqlite

import (
	"context"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
)

const markClosedSQL = `INSERT INTO extraction_cursors (provider, unit_key, closure_marked, at) VALUES (?, ?, 1, ?)
	ON CONFLICT (provider, unit_key) DO UPDATE SET closure_marked = 1`

// MarkClosed records a sent draft's unit.closed on u, which no extraction
// pass clears.
func (s *ExtractionStore) MarkClosed(ctx context.Context, u unit.Unit, at time.Time) error {
	_, err := s.db.Write(ctx, func(tx *Tx) error {
		tx.Touch(port.TopicSourceEvents, u)
		_, err := tx.ExecContext(ctx, markClosedSQL, string(u.Provider), u.Key, formatTime(at))
		return err
	})
	return err
}
