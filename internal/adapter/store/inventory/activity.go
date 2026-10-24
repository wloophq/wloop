package inventory

import (
	"context"
	"strings"
	"time"

	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
)

// Activity is what the loop spent on a pull request: its turns, the newest
// one's intent, their tokens and cost, and when its brief retries.
type Activity struct {
	Turns, OpenTurns int
	LastIntent       string
	Tokens           int64
	CostUSD          float64
	BriefRetryAt     time.Time
}

// Activities reads the activity of every pull request that has any, in
// one statement: one aggregated part per table.
func (s *Store) Activities(ctx context.Context) (map[github.Ref]Activity, error) {
	out := map[github.Ref]Activity{}
	// SQLite takes the bare intent from the row of max(seq).
	err := s.eachRef(ctx, `SELECT unit_key, 'turns', count(*), sum(state IN ('sent', 'started', 'asked')), intent, max(seq), 0.0, ''
		FROM turns WHERE provider = 'github' GROUP BY unit_key
		UNION ALL
		SELECT t.unit_key, 'usage', 0, 0, '', sum(u.input_tokens + u.output_tokens), total(u.cost_usd), ''
		FROM turn_usage u JOIN turns t ON t.seq = u.turn_seq WHERE t.provider = 'github' GROUP BY t.unit_key
		UNION ALL
		SELECT `+unitKeyOf("b")+`, 'brief', 0, 0, '', 0, 0.0, retry_at FROM pr_briefs b WHERE retry_at != ''`, func(scan scanRef) error {
		var part, intent, retry string
		var turns, open int
		var n int64
		var cost float64
		r, err := scan(&part, &turns, &open, &intent, &n, &cost, &retry)
		if err != nil {
			return err
		}
		a := out[r]
		switch part {
		case "turns":
			a.Turns, a.OpenTurns, a.LastIntent = turns, open, intent
		case "usage":
			a.Tokens, a.CostUSD = n, cost
		case "brief":
			if a.BriefRetryAt, err = parseTS(retry); err != nil {
				return err
			}
		}
		out[r] = a
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// scanRef scans a row whose first column is a github unit key into its ref
// and the rest into dst.
type scanRef func(dst ...any) (github.Ref, error)

func (s *Store) eachRef(ctx context.Context, q string, each func(scanRef) error) error {
	rows, err := s.db.R().QueryContext(ctx, q)
	if err != nil {
		return err
	}
	defer rows.Close()
	scan := func(dst ...any) (github.Ref, error) {
		var key string
		err := rows.Scan(append([]any{&key}, dst...)...)
		return refOf(string(unit.GitHub), key), err
	}
	for rows.Next() {
		if err := each(scan); err != nil {
			return err
		}
	}
	return rows.Err()
}

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
