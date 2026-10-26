package inventory

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"time"

	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
)

const prsChunk = 300

// MetaTimingsSince is the meta key of when timing records began.
const MetaTimingsSince = "timings_since"

const outagesSQL = `SELECT count(*) FROM events WHERE kind = 'brief_breaker_opened' AND at >= ?`

// Timings reads the record of the window since since.
func (s *Store) Timings(ctx context.Context, since time.Time) (dinv.TimingsData, error) {
	var d dinv.TimingsData
	at := ts(since)
	raw, err := s.Meta(ctx, MetaTimingsSince)
	if err != nil {
		return d, err
	}
	if d.RecordedSince, err = parseTS(raw); err != nil {
		return d, err
	}
	if d.Turns, err = s.timedTurns(ctx, at); err != nil {
		return d, err
	}
	if d.Tokens, err = s.turnTokens(ctx, at); err != nil {
		return d, err
	}
	if d.Intakes, err = s.timedIntakes(ctx, at); err != nil {
		return d, err
	}
	if d.Runs, err = s.BriefRuns(ctx, since); err != nil {
		return d, err
	}
	if err := s.db.R().QueryRowContext(ctx, outagesSQL, at).Scan(&d.Outages); err != nil {
		return d, err
	}
	refs, err := s.activeRefs(ctx, at)
	if err != nil {
		return d, err
	}
	known, err := s.prsByRef(ctx, refs)
	if err != nil {
		return d, err
	}
	for _, ref := range refs {
		p, ok := known[ref]
		if !ok {
			p = dinv.PR{Ref: ref}
		}
		d.PRs = append(d.PRs, p)
	}
	if d.Events, err = s.EventsByPR(ctx, refs); err != nil {
		return d, err
	}
	if d.Tags, err = s.tagTallies(ctx, at); err != nil {
		return d, err
	}
	if d.PRTags, err = s.prTags(ctx, at); err != nil {
		return d, err
	}
	d.PRFiles, err = s.prFiles(ctx, at)
	return d, err
}

func (s *Store) prsByRef(ctx context.Context, refs []github.Ref) (map[github.Ref]dinv.PR, error) {
	out := make(map[github.Ref]dinv.PR, len(refs))
	for chunk := range slices.Chunk(refs, prsChunk) {
		var where []string
		var args []any
		for _, r := range chunk {
			where = append(where, `(owner=? AND repo=? AND number=?)`)
			args = append(args, r.Owner, r.Repo, r.Number)
		}
		rows, err := s.db.R().QueryContext(ctx, `SELECT `+prColumns+` FROM pull_requests pr WHERE `+strings.Join(where, " OR "), args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			p, err := scanPR(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			out[p.Ref] = p
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// activeIn selects the github rows, keyed by unit, of the pull requests
// active in the window bound to its one parameter.
const activeIn = `provider = 'github' AND unit_key IN (` + activeRefsSQL + `)`

// prTags are the tags of the pull requests active in the window.
func (s *Store) prTags(ctx context.Context, at string) (map[github.Ref][]string, error) {
	rows, err := s.db.R().QueryContext(ctx, `SELECT unit_key, key, value FROM unit_tags WHERE `+activeIn+` ORDER BY unit_key, key, value`, at)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[github.Ref][]string{}
	for rows.Next() {
		var key, k, v string
		if err := rows.Scan(&key, &k, &v); err != nil {
			return nil, err
		}
		r, err := pr.ParseKey(key)
		if err != nil {
			return nil, err
		}
		out[r] = append(out[r], k+":"+v)
	}
	return out, rows.Err()
}

// prFiles are the files of the briefs in use of the pull requests active in
// the window.
func (s *Store) prFiles(ctx context.Context, at string) (map[github.Ref][]string, error) {
	rows, err := s.db.R().QueryContext(ctx, `SELECT b.unit_key, COALESCE(json_extract(r.input, '$.files'), '') FROM unit_briefs b JOIN brief_runs r ON r.id = b.run
		WHERE b.run > 0 AND b.provider = 'github' AND b.unit_key IN (`+activeRefsSQL+`)`, at)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[github.Ref][]string{}
	for rows.Next() {
		var key, raw string
		if err := rows.Scan(&key, &raw); err != nil {
			return nil, err
		}
		r, err := pr.ParseKey(key)
		if err != nil {
			return nil, err
		}
		files, err := splitList(raw)
		if err != nil {
			return nil, err
		}
		if len(files) > 0 {
			out[r] = files
		}
	}
	return out, rows.Err()
}

const activeRefsSQL = `SELECT DISTINCT unit_key FROM events WHERE at >= ? AND +provider = 'github'`

func (s *Store) activeRefs(ctx context.Context, at string) ([]github.Ref, error) {
	rows, err := s.db.R().QueryContext(ctx, activeRefsSQL, at)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []github.Ref
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		out = append(out, refOf(string(unit.GitHub), key))
	}
	slices.SortFunc(out, compareRefs)
	return out, rows.Err()
}

const timedTurnsSQL = `SELECT seq, provider, unit_key, intent, trigger, state, agent, sent_at, started_at, ended_at
	FROM turns INDEXED BY turns_sent_at WHERE sent_at >= ? ORDER BY seq`

func (s *Store) timedTurns(ctx context.Context, at string) ([]dinv.TimedTurn, error) {
	rows, err := s.db.R().QueryContext(ctx, timedTurnsSQL, at)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dinv.TimedTurn
	for rows.Next() {
		var t dinv.TimedTurn
		var provider, key, sent, started, ended string
		if err := rows.Scan(&t.Seq, &provider, &key, &t.Intent, &t.Trigger, &t.State, &t.Agent, &sent, &started, &ended); err != nil {
			return nil, err
		}
		t.PR = refOf(provider, key)
		if err := parseAll([]string{sent, started, ended}, &t.SentAt, &t.StartedAt, &t.EndedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

//nolint:gosec // G101: a query that names token-count columns, not a credential
const turnTokensSQL = `SELECT u.turn_seq, u.model, u.input_tokens, u.output_tokens, u.cache_read_tokens, u.cache_write_tokens, u.cost_usd
	FROM turn_usage u JOIN turns t ON t.seq = u.turn_seq WHERE t.sent_at >= ? ORDER BY u.turn_seq, u.model`

func (s *Store) turnTokens(ctx context.Context, at string) ([]dinv.TurnTokens, error) {
	rows, err := s.db.R().QueryContext(ctx, turnTokensSQL, at)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dinv.TurnTokens
	for rows.Next() {
		var u dinv.TurnTokens
		var cost sql.Null[float64]
		if err := rows.Scan(&u.TurnSeq, &u.Model, &u.Input, &u.Output, &u.CacheRead, &u.CacheWrite, &cost); err != nil {
			return nil, err
		}
		if cost.Valid {
			u.CostUSD = &cost.V
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) timedIntakes(ctx context.Context, at string) ([]dinv.TimedIntake, error) {
	rows, err := s.db.R().QueryContext(ctx, `SELECT seq, provider, unit_key, intent, source, state, brief, accepted_at, starting_at, ended_at
		FROM intakes WHERE accepted_at >= ? ORDER BY seq`, at)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dinv.TimedIntake
	for rows.Next() {
		var i dinv.TimedIntake
		var provider, key, accepted, starting, ended string
		if err := rows.Scan(&i.Seq, &provider, &key, &i.Intent, &i.Source, &i.State, &i.Brief, &accepted, &starting, &ended); err != nil {
			return nil, err
		}
		i.PR = refOf(provider, key)
		if err := parseAll([]string{accepted, starting, ended}, &i.AcceptedAt, &i.StartingAt, &i.EndedAt); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

const tagTalliesSQL = `SELECT key, value, count(*) FROM unit_tags WHERE ` + activeIn + `
	GROUP BY key, value ORDER BY key, count(*) DESC, value`

// tagTallies counts the tags of the pull requests active in the window.
func (s *Store) tagTallies(ctx context.Context, at string) ([]dinv.TagTally, error) {
	rows, err := s.db.R().QueryContext(ctx, tagTalliesSQL, at)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dinv.TagTally
	for rows.Next() {
		var t dinv.TagTally
		if err := rows.Scan(&t.Key, &t.Value, &t.Count); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// parseAll reads times the turn store writes in RFC 3339 with trimmed
// nanoseconds, which also reads the fixed-width event layout.
func parseAll(raw []string, dst ...*time.Time) error {
	for n, r := range raw {
		if r == "" {
			*dst[n] = time.Time{}
			continue
		}
		v, err := time.Parse(time.RFC3339Nano, r)
		if err != nil {
			return err
		}
		*dst[n] = v
	}
	return nil
}
