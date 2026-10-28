package inventory

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/port"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/github"
)

type scope struct {
	reasons  []string
	followed bool
	ended    time.Time
}

// scopeOf is the scope of a stored row: followed when it is open and
// PR.Followed holds; ended is when a merged or closed one finished.
func scopeOf(reasons []string, ws, state, ended string) (scope, error) {
	end, err := parseTS(ended)
	if err != nil {
		return scope{}, err
	}
	open := state == dgh.StateOpen
	return scope{reasons: reasons, followed: open && dinv.PR{Reasons: reasons, Workspace: dinv.Workspace{ID: ws}}.Followed(), ended: end}, nil
}

// scopes reads the scope of each of refs, or of every pull request when refs
// is empty.
func scopes(ctx context.Context, tx *sqlite.Tx, refs ...github.Ref) (map[github.Ref]scope, error) {
	query, args := `SELECT owner, repo, number, reasons, ws_id, state, iif(merged_at != '', merged_at, closed_at) FROM pull_requests`, []any(nil)
	if len(refs) > 0 {
		var marks string
		marks, args = refValues(refs)
		query += ` WHERE (owner, repo, number) IN (VALUES ` + marks + `)`
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[github.Ref]scope{}
	for rows.Next() {
		var ref github.Ref
		var reasons, ws, state, ended string
		if err := rows.Scan(&ref.Owner, &ref.Repo, &ref.Number, &reasons, &ws, &state, &ended); err != nil {
			return nil, err
		}
		list, err := splitList(reasons)
		if err != nil {
			return nil, err
		}
		if out[ref], err = scopeOf(list, ws, state, ended); err != nil {
			return nil, err
		}
	}
	return out, rows.Err()
}

// flipping is a pull request whose followed state flipped in a write.
type flipping struct {
	ref      github.Ref
	was, now scope
	at       time.Time
}

// scopeFlips are the pull requests among refs, every one when empty,
// whose followed state flipped since before, at seen, else now.
func (s *Store) scopeFlips(ctx context.Context, tx *sqlite.Tx, before map[github.Ref]scope, seen time.Time, refs ...github.Ref) ([]flipping, error) {
	after, err := scopes(ctx, tx, refs...)
	if err != nil {
		return nil, err
	}
	var out []flipping
	at := seen.UTC()
	for ref, now := range after {
		was := before[ref]
		if now.followed == was.followed {
			continue
		}
		if at.IsZero() {
			at = s.clock().UTC()
		}
		out = append(out, flipping{ref, was, now, at})
	}
	return out, nil
}

// recordFlips records the followed or unfollowed event of each of flips on
// the host, an unfollow dated after the last followed event.
func (s *Store) recordFlips(ctx context.Context, flips []flipping) error {
	if len(flips) == 0 {
		return nil
	}
	var unfollowed []github.Ref
	for _, f := range flips {
		if f.was.followed {
			unfollowed = append(unfollowed, f.ref)
		}
	}
	last, err := s.lastFollowed(ctx, unfollowed)
	if err != nil {
		return err
	}
	evs := make([]dinv.Event, len(flips))
	for i, f := range flips {
		evs[i] = flip(f.ref, f.was, f.now, f.at, last[f.ref])
	}
	return s.recordEvents(ctx, evs)
}

// flip is the followed or unfollowed event of ref's scope going from was to
// now at at. A merge or close is dated when it happened, but never before
// followedAt, the latest followed event, so an interval in the loop never
// ends before it starts: a merge GitHub dates before the loop first saw the
// pull request ends it at once.
func flip(ref github.Ref, was, now scope, at, followedAt time.Time) dinv.Event {
	if now.followed {
		return dinv.Event{PR: ref, Kind: dinv.KindFollowed, Key: ts(at), At: at, Data: strings.Join(now.reasons, ",")}
	}
	end := at
	if !now.ended.IsZero() {
		end = now.ended.UTC()
	}
	if followedAt.After(end) {
		end = followedAt
	}
	return dinv.Event{PR: ref, Kind: dinv.KindUnfollowed, Key: ts(end), At: end, Data: strings.Join(was.reasons, ",")}
}

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
