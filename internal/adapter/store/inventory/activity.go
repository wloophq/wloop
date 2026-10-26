package inventory

import (
	"context"
	"strings"

	dinv "github.com/wloophq/wloop/internal/domain/inventory"
)

func recentActivitySQL(marks string) string {
	return `SELECT p.owner, p.repo, p.number, e.kind, e.key, e.actor, e.at, e.data, p.title, p.url, p.author, p.state
		FROM events e JOIN pull_requests p ON +e.provider = 'github' AND ` + prOfUnit("e.unit_key") + `
		WHERE e.kind NOT IN (` + marks + `) ORDER BY e.at DESC, e.kind, e.key LIMIT ?`
}

// RecentActivity returns the newest events of the pull requests in the
// inventory with their title, newest first, in one query. Bookkeeping events
// and events of pull requests the inventory does not hold are left out.
func (s *Store) RecentActivity(ctx context.Context, limit int) ([]dinv.ActivityEvent, error) {
	args := make([]any, 0, len(dinv.BookkeepingKinds())+1)
	for _, k := range dinv.BookkeepingKinds() {
		args = append(args, k)
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(dinv.BookkeepingKinds())), ",")
	rows, err := s.db.R().QueryContext(ctx, recentActivitySQL(marks), append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dinv.ActivityEvent
	for rows.Next() {
		var a dinv.ActivityEvent
		var at string
		if err := rows.Scan(&a.PR.Owner, &a.PR.Repo, &a.PR.Number, &a.Kind, &a.Key, &a.Actor, &at, &a.Data, &a.Title, &a.URL, &a.Author, &a.State); err != nil {
			return nil, err
		}
		if a.At, err = parseTS(at); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
