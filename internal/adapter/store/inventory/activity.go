package inventory

import (
	"context"
	"strings"

	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// recentActivitySQL reads a pull request event's title, URL, author and
// state from its pull request row and another unit's from its card; the
// first and the next to last arguments are GitHub's provider id.
func recentActivitySQL(marks string) string {
	return `SELECT e.provider, e.unit_key, e.kind, e.key, e.actor, e.at, e.data,
		COALESCE(p.title, c.card ->> '$.title', ''), COALESCE(p.url, c.card ->> '$.link', ''), COALESCE(p.author, ''),
		COALESCE(p.state, c.card ->> '$.state', ''), p.number IS NULL AND COALESCE(c.card ->> '$.started_by_me', 0)
		FROM events e
		LEFT JOIN pull_requests p ON e.provider = ? AND ` + prOfUnit("e.unit_key") + `
		LEFT JOIN unit_cards c ON c.provider = e.provider AND c.unit_key = e.unit_key
		WHERE e.kind NOT IN (` + marks + `) AND (p.number IS NOT NULL OR e.provider != ? AND c.unit_key IS NOT NULL)
		ORDER BY e.at DESC, e.kind, e.key LIMIT ?`
}

// RecentActivity returns the newest events of the units the store holds a
// pull request row or a card of, with their title, newest first, in one
// query. Bookkeeping events and events of other units are left out.
func (s *Store) RecentActivity(ctx context.Context, limit int) ([]dinv.ActivityEvent, error) {
	kinds := dinv.BookkeepingKinds()
	forge := string(unit.GitHub)
	args := make([]any, 0, len(kinds)+3)
	args = append(args, forge)
	for _, k := range kinds {
		args = append(args, k)
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(kinds)), ",")
	rows, err := s.db.R().QueryContext(ctx, recentActivitySQL(marks), append(args, forge, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dinv.ActivityEvent
	for rows.Next() {
		var a dinv.ActivityEvent
		var provider, at string
		if err := rows.Scan(&provider, &a.Unit.Key, &a.Kind, &a.Key, &a.Actor, &at, &a.Data, &a.Title, &a.URL, &a.Author, &a.State, &a.Mine); err != nil {
			return nil, err
		}
		a.Unit.Provider, a.PR = unit.ProviderID(provider), refOf(provider, a.Unit.Key)
		if a.At, err = parseTS(at); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
