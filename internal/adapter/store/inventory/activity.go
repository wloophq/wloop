package inventory

import (
	"context"
	"strings"

	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
)

// recentActivitySQL reads the newest events of GitHub units and of units
// with a card, another unit's title, URL and state from its card; the
// first and the next to last arguments are GitHub's provider id.
func recentActivitySQL(marks string) string {
	return `SELECT e.provider, e.unit_key, e.kind, e.key, e.actor, e.at, e.data,
		COALESCE(c.card ->> '$.title', ''), COALESCE(c.card ->> '$.link', ''), COALESCE(c.card ->> '$.state', ''),
		e.provider != ?1 AND COALESCE(c.card ->> '$.started_by_me', 0)
		FROM events e
		LEFT JOIN unit_cards c ON c.provider = e.provider AND c.unit_key = e.unit_key
		WHERE e.kind NOT IN (` + marks + `) AND (e.provider = ?1 OR c.unit_key IS NOT NULL)
		ORDER BY e.at DESC, e.kind, e.key LIMIT ? OFFSET ?`
}

// RecentActivity returns the newest events of the units the store holds a
// pull request row or a card of, with their title, newest first: a page of
// events at a time, the pull requests of a page read apart. Bookkeeping
// events and events of other units are left out.
func (s *Store) RecentActivity(ctx context.Context, limit int) ([]dinv.ActivityEvent, error) {
	kinds := dinv.BookkeepingKinds()
	forge := unit.GitHub
	args := make([]any, 0, len(kinds)+3)
	args = append(args, string(forge))
	for _, k := range kinds {
		args = append(args, k)
	}
	query := recentActivitySQL(strings.TrimSuffix(strings.Repeat("?,", len(kinds)), ","))
	page := max(limit, 1)
	var out []dinv.ActivityEvent
	for offset := 0; len(out) < limit; offset += page {
		got, err := s.activityPage(ctx, query, append(args, page, offset)...)
		if err != nil {
			return nil, err
		}
		var refs []github.Ref
		for _, a := range got {
			if a.Unit.Provider == forge {
				refs = append(refs, a.PR)
			}
		}
		prs, err := s.reportedPRs(ctx, refs)
		if err != nil {
			return nil, err
		}
		for _, a := range got {
			if a.Unit.Provider == forge {
				p, ok := prs[a.PR]
				if !ok {
					continue
				}
				a.Title, a.URL, a.Author, a.State = p.Title, p.URL, p.Author, p.State
			}
			if out = append(out, a); len(out) == limit {
				break
			}
		}
		if len(got) < page {
			break
		}
	}
	return out, nil
}

func (s *Store) activityPage(ctx context.Context, query string, args ...any) ([]dinv.ActivityEvent, error) {
	rows, err := s.db.R().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dinv.ActivityEvent
	for rows.Next() {
		var a dinv.ActivityEvent
		var provider, at string
		if err := rows.Scan(&provider, &a.Unit.Key, &a.Kind, &a.Key, &a.Actor, &at, &a.Data, &a.Title, &a.URL, &a.State, &a.Mine); err != nil {
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
