package inventory

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/port"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
)

const lastSituationSQL = `SELECT data, CAST(key AS INTEGER) FROM events WHERE provider = ? AND unit_key = ? AND kind = ?
	ORDER BY CAST(key AS INTEGER) DESC LIMIT 1`

// RecordSituations writes, in one transaction, a situation event for each
// situation whose data differs from its pull request's last one. The key is
// the pull request's next situation number, so neither a clock step nor two
// equal times can lose or reorder one.
func (s *Store) RecordSituations(ctx context.Context, sits []dinv.Situation) error {
	if len(sits) == 0 {
		return nil
	}
	_, err := s.db.Write(ctx, func(tx *sqlite.Tx) error {
		for _, sit := range sits {
			ref := sit.PR
			var last string
			var n int64
			err := tx.QueryRowContext(ctx, lastSituationSQL, append(unitArgs(ref), dinv.KindSituation)...).Scan(&last, &n)
			switch {
			case errors.Is(err, sql.ErrNoRows):
			case err != nil:
				return err
			case last == sit.Data:
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO events (provider, unit_key, kind, key, actor, at, data) VALUES (?, ?, ?, ?, '', ?, ?)`,
				append(unitArgs(ref), dinv.KindSituation, strconv.FormatInt(n+1, 10), ts(sit.At), sit.Data)...); err != nil {
				return err
			}
			tx.Touch(port.TopicLoopEvents, ref.Unit())
		}
		return nil
	})
	return err
}
