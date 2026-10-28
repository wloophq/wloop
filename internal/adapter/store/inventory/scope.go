package inventory

import (
	"context"
	"database/sql"
	"errors"
	"slices"
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
	ws       string
	open     bool
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
	return scope{reasons: reasons, ws: ws, open: state == dgh.StateOpen, ended: end}.with(reasons, ws), nil
}

// with is sc with reasons and the workspace ws.
func (sc scope) with(reasons []string, ws string) scope {
	sc.reasons, sc.ws = reasons, ws
	sc.followed = sc.open && dinv.PR{Reasons: reasons, Workspace: dinv.Workspace{ID: ws}}.Followed()
	return sc
}

// scopes reads the scope of each of refs, or of every pull request when refs
// is empty.
func scopes(ctx context.Context, tx sqlite.Reader, refs ...github.Ref) (map[github.Ref]scope, error) {
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

// flipLog is what the host holds of pull requests' scope flips: the kind
// of each one's last flip and the time of its last followed event.
type flipLog struct {
	last       map[github.Ref]string
	lastAt     map[github.Ref]time.Time
	followedAt map[github.Ref]time.Time
}

// saw adds e to the log when it is a flip.
func (l *flipLog) saw(e dinv.Event) {
	if e.Kind != dinv.KindFollowed && e.Kind != dinv.KindUnfollowed {
		return
	}
	if l.last == nil {
		l.last, l.lastAt, l.followedAt = map[github.Ref]string{}, map[github.Ref]time.Time{}, map[github.Ref]time.Time{}
	}
	if !e.At.Before(l.lastAt[e.PR]) {
		l.last[e.PR], l.lastAt[e.PR] = e.Kind, e.At
	}
	if e.Kind == dinv.KindFollowed && e.At.After(l.followedAt[e.PR]) {
		l.followedAt[e.PR] = e.At
	}
}

// wants says whether e, a flip, is one the host lacks: a flip of the kind
// the host's last one already has is a retry of a write that failed after
// the host took it.
func (l *flipLog) wants(e dinv.Event) bool { return l.last[e.PR] != e.Kind }

// flipLogOf reads the host's flips of refs.
func (s *Store) flipLogOf(ctx context.Context, refs []github.Ref) (flipLog, error) {
	evs, err := s.hostEvents(ctx, refs, dinv.KindFollowed, dinv.KindUnfollowed)
	if err != nil {
		return flipLog{}, err
	}
	var l flipLog
	for _, e := range evs {
		l.saw(e)
	}
	return l, nil
}

// recordFlipsOf records on the host, before the write that makes them, the
// flip of each of refs the write turns from its stored scope, zero when it
// has no row, into what change says, at at, else now; change says false
// for a ref the write leaves.
func (s *Store) recordFlipsOf(ctx context.Context, refs []github.Ref, at time.Time, change func(github.Ref, scope) (scope, bool, error)) error {
	before, err := scopes(ctx, s.db.R(), refs...)
	if err != nil {
		return err
	}
	var flipped []github.Ref
	after := map[github.Ref]scope{}
	for _, ref := range refs {
		was := before[ref]
		now, ok, err := change(ref, was)
		if err != nil {
			return err
		}
		if ok && now.followed != was.followed {
			flipped = append(flipped, ref)
			after[ref] = now
		}
	}
	if len(flipped) == 0 {
		return nil
	}
	slices.SortFunc(flipped, compareRefs)
	log, err := s.flipLogOf(ctx, flipped)
	if err != nil {
		return err
	}
	at = at.UTC()
	if at.IsZero() {
		at = s.clock().UTC()
	}
	var evs []dinv.Event
	for _, ref := range flipped {
		e := flip(ref, before[ref], after[ref], at, log.followedAt[ref])
		if log.wants(e) {
			evs = append(evs, e)
		}
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
