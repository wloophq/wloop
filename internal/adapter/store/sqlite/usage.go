package sqlite

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/usage"
	"github.com/wloophq/wloop/internal/domain/unit"
	dusage "github.com/wloophq/wloop/internal/domain/usage"
)

type usageStore struct{ db *DB }

var _ usage.Store = usageStore{}

func (db *DB) Usage() usage.Store { return usageStore{db} }

// Pending lists the closed session turns ended at or after endedSince whose usage
// is not read, fewest failed reads first, then oldest, with their agent
// session's paths. ended_at is in TimeLayout, which does not sort as text,
// so julianday compares it.
func (u usageStore) Pending(ctx context.Context, endedSince time.Time, limit int) ([]usage.Turn, error) {
	rows, err := u.db.R().QueryContext(ctx, `SELECT t.seq, t.provider, t.unit_key, t.agent, t.agent_session,
		COALESCE(a.transcript, ''), COALESCE(a.home, ''), COALESCE(a.worktree, ''), t.sent_at, t.started_at, t.ended_at, t.usage_attempts
		FROM turns t LEFT JOIN agent_sessions a ON t.agent_session != '' AND a.agent_session = t.agent_session
		WHERE t.state NOT IN ('sent', 'started', 'asked', 'withdrawn') AND t.ended_at != '' AND t.usage_source = '' AND t.mode != 'headless'
		AND julianday(t.ended_at) >= julianday(?)
		ORDER BY t.usage_attempts, julianday(t.ended_at), t.seq LIMIT ?`, endedSince.UTC().Format(TimeLayout), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []usage.Turn
	for rows.Next() {
		var t usage.Turn
		var u unit.Unit
		var sent, started, ended string
		if err := rows.Scan(&t.Seq, &u.Provider, &u.Key, &t.Kind, &t.AgentSession,
			&t.Transcript, &t.Home, &t.Worktree, &sent, &started, &ended, &t.Attempts); err != nil {
			return nil, err
		}
		t.PR, t.From, t.To = prOf(u), parseTime(cmp.Or(started, sent)), parseTime(ended)
		out = append(out, t)
	}
	return out, rows.Err()
}

// SaveUsage replaces the turn's per-model rows and marks it read by source,
// with evs, in one transaction.
func (u usageStore) SaveUsage(ctx context.Context, seq int64, rows []dusage.Model, source string, at time.Time, evs ...port.Event) error {
	return New(u.db).write(ctx, evs, func(tx *Tx) error {
		if err := touchTurn(ctx, tx, `UPDATE turns SET usage_source = ?, usage_read_at = ? WHERE seq = ? RETURNING provider, unit_key`,
			seq, source, eventTime(at), seq); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM turn_usage WHERE turn_seq = ?`, seq); err != nil {
			return err
		}
		for _, m := range rows {
			if _, err := tx.ExecContext(ctx, `INSERT INTO turn_usage (turn_seq, model, messages, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd, first_at, last_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, seq, m.Model, m.Messages, m.Tokens.Input, m.Tokens.Output, m.Tokens.CacheRead, m.Tokens.CacheWrite,
				nullFloat(m.CostUSD), eventTime(m.First), eventTime(m.Last)); err != nil {
				return err
			}
		}
		return nil
	})
}

func (u usageStore) Attempted(ctx context.Context, seq int64) error {
	_, err := u.db.Write(ctx, func(tx *Tx) error {
		return touchTurn(ctx, tx, `UPDATE turns SET usage_attempts = usage_attempts + 1 WHERE seq = ? RETURNING provider, unit_key`, seq, seq)
	})
	return err
}

// touchTurn runs update, which returns the unit of turn seq, and touches
// Usage for it.
func touchTurn(ctx context.Context, tx *Tx, update string, seq int64, args ...any) error {
	var u unit.Unit
	err := tx.QueryRowContext(ctx, update, args...).Scan(&u.Provider, &u.Key)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("no turn %d", seq)
	}
	if err != nil {
		return err
	}
	tx.Touch(port.TopicUsage, u)
	return nil
}

func nullFloat(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

// HeadlessSpent is the cost in turn_usage of the headless turns started in [from, to).
func (db *DB) HeadlessSpent(ctx context.Context, from, to time.Time) (float64, error) {
	var spent float64
	err := db.R().QueryRowContext(ctx, `SELECT COALESCE(SUM(u.cost_usd), 0) FROM turns t JOIN turn_usage u ON u.turn_seq = t.seq
		WHERE t.mode = 'headless' AND julianday(t.started_at) >= julianday(?) AND julianday(t.started_at) < julianday(?)`,
		from.UTC().Format(TimeLayout), to.UTC().Format(TimeLayout)).Scan(&spent)
	return spent, err
}
