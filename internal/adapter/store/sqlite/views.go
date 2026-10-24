package sqlite

import (
	"context"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/view"
)

func (s *Store) Applied(ctx context.Context, sink string) (map[view.Key]view.Applied, error) {
	rows, err := s.db.R().QueryContext(ctx, `SELECT provider, unit_key, role, status, unread, summary, col, override, at FROM view_applied WHERE sink = ?`, sink)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[view.Key]view.Applied{}
	for rows.Next() {
		var a view.Applied
		var at string
		if err := rows.Scan(&a.Unit.Provider, &a.Unit.Key, &a.Role, &a.Status, &a.Unread, &a.Summary, &a.Column, &a.Override, &at); err != nil {
			return nil, err
		}
		a.At = parseTime(at)
		out[a.Key()] = a
	}
	return out, rows.Err()
}

func (s *Store) SaveApplied(ctx context.Context, sink string, a view.Applied, evs ...port.Event) error {
	return s.write(ctx, evs, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO view_applied (sink, provider, unit_key, role, status, unread, summary, col, override, at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			sink, string(a.Unit.Provider), a.Unit.Key, string(a.Role), string(a.Status), a.Unread, a.Summary, a.Column, a.Override, formatTime(a.At))
		if err != nil {
			return err
		}
		tx.Touch(port.TopicViews, a.Unit)
		return nil
	})
}

func (s *Store) SaveFailure(ctx context.Context, evs ...port.Event) error {
	return s.write(ctx, evs, func(*Tx) error { return nil })
}
