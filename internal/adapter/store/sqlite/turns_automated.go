package sqlite

import (
	"context"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// AutomatedTurns counts the automated turns sent since from on units of
// providers other than except's, withdrawn ones included: a turn a workflow
// started spends the day's budget whatever became of it.
func (db *DB) AutomatedTurns(ctx context.Context, from time.Time, except []unit.ProviderID) (int, error) {
	q := `SELECT COUNT(*) FROM turns WHERE trigger = ? AND julianday(sent_at) >= julianday(?)`
	args := []any{string(turn.Automated), from.UTC().Format(TimeLayout)}
	if len(except) > 0 {
		q += ` AND provider NOT IN (` + strings.TrimSuffix(strings.Repeat("?, ", len(except)), ", ") + `)`
		for _, p := range except {
			args = append(args, string(p))
		}
	}
	var n int
	err := db.R().QueryRowContext(ctx, q, args...).Scan(&n)
	return n, err
}
