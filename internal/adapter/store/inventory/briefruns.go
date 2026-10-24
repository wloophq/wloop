package inventory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/port"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/tag"
	"github.com/wloophq/wloop/internal/domain/usage"
	"github.com/wloophq/wloop/internal/github"
)

func nullable[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}

func pointer[T any](n sql.Null[T]) *T {
	if !n.Valid {
		return nil
	}
	return &n.V
}

// StartBriefRun stores r as running and returns its id.
func (s *Store) StartBriefRun(ctx context.Context, r dinv.BriefRun) (int64, error) {
	var id int64
	_, err := s.db.Write(ctx, func(tx *sqlite.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO brief_runs (owner, repo, number, trigger, intake, version, attempt_of, head_oid, body_edited_at, changed_files, started_at, outcome)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.PR.Owner, r.PR.Repo, r.PR.Number, r.Trigger, r.Intake, r.Version, r.AttemptOf, r.HeadOid, ts(r.BodyEditedAt), r.ChangedFiles,
			ts(r.StartedAt), outcomeRunning)
		if err != nil {
			return err
		}
		tx.Touch(port.TopicBriefs, r.PR.Unit())
		id, err = res.LastInsertId()
		return err
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}

// EndBriefRun updates r, sets pr_briefs.last_run (and run, failures cleared, on ok or reused; a reused run carries the brief, tags and model of ReusedFrom) and the state fields of st, and for an ok run replaces pr_tags with r.Tags, in one transaction with KindBriefEnded and, when st newly gives up, KindBriefGaveUp.
func (s *Store) EndBriefRun(ctx context.Context, r dinv.BriefRun, st dinv.BriefRecord) error {
	if r.ID == 0 || r.EndedAt.IsZero() {
		return fmt.Errorf("ending a brief run of %s: no id or end time", r.PR)
	}
	_, err := s.db.Write(ctx, func(tx *sqlite.Tx) error {
		if r.Outcome == outcomeReused {
			if err := carryBrief(ctx, tx, &r); err != nil {
				return err
			}
		}
		var in, out, read, write *int64
		if r.Tokens != nil {
			in, out, read, write = &r.Tokens.Input, &r.Tokens.Output, &r.Tokens.CacheRead, &r.Tokens.CacheWrite
		}
		res, err := tx.ExecContext(ctx, `UPDATE brief_runs SET trigger = ?, intake = ?, version = ?, attempt_of = ?, head_oid = ?, body_edited_at = ?, changed_files = ?,
			started_at = ?, ended_at = ?, input_ms = ?, call_ms = ?, agent_ms = ?, outcome = ?, error = ?, model = ?, cost_usd = ?,
			input_tokens = ?, output_tokens = ?, cache_read_tokens = ?, cache_write_tokens = ?, diff_bytes = ?, diff_cut = ?,
			brief = ?, files = ?, tags = ?, dropped_tags = ?, input_sha = ?, reused_from = ?
			WHERE id = ? AND owner = ? AND repo = ? AND number = ?`,
			r.Trigger, r.Intake, r.Version, r.AttemptOf, r.HeadOid, ts(r.BodyEditedAt), r.ChangedFiles,
			ts(r.StartedAt), ts(r.EndedAt), nullable(r.InputMS), nullable(r.CallMS), nullable(r.AgentMS), r.Outcome, r.Error, r.Model, nullable(r.CostUSD),
			nullable(in), nullable(out), nullable(read), nullable(write), nullable(r.DiffBytes), r.DiffCut,
			r.Brief, joinList(r.Files), joinList(r.Tags), joinList(r.DroppedTags), r.InputSHA, r.ReusedFrom,
			r.ID, r.PR.Owner, r.PR.Repo, r.PR.Number)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n != 1 {
			return fmt.Errorf("%s has no brief run %d", r.PR, r.ID)
		}
		tx.Touch(port.TopicBriefs, r.PR.Unit())
		var gaveUp string
		err = tx.QueryRowContext(ctx, `SELECT gave_up_at FROM pr_briefs WHERE owner = ? AND repo = ? AND number = ?`, r.PR.Owner, r.PR.Repo, r.PR.Number).Scan(&gaveUp)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		ok := r.Outcome == outcomeOK || r.Outcome == outcomeReused
		failures := st.Failures
		if ok {
			failures = 0
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO pr_briefs (owner, repo, number, failures, attempt_of, run, last_run, wanted_at, retry_at, gave_up_at, gave_up_reason)
			VALUES (?1, ?2, ?3, ?4, ?5, iif(?6, ?7, 0), ?7, ?8, ?9, ?10, ?11)
			ON CONFLICT (owner, repo, number) DO UPDATE SET failures = excluded.failures, attempt_of = excluded.attempt_of,
			run = iif(?6, ?7, run), last_run = excluded.last_run, wanted_at = excluded.wanted_at, retry_at = excluded.retry_at,
			gave_up_at = excluded.gave_up_at, gave_up_reason = excluded.gave_up_reason`,
			r.PR.Owner, r.PR.Repo, r.PR.Number, failures, st.AttemptOf, ok, r.ID, ts(st.WantedAt), ts(st.RetryAt), ts(st.GaveUpAt), st.GaveUpReason); err != nil {
			return err
		}
		if ok {
			if err := replaceTags(ctx, tx, r.PR, r.ID, r.Tags); err != nil {
				return err
			}
		}
		evs := []dinv.Event{{PR: r.PR, Kind: dinv.KindBriefEnded, Key: strconv.FormatInt(r.ID, 10), At: r.EndedAt, Data: r.Outcome}}
		if gaveUp == "" && !st.GaveUpAt.IsZero() {
			evs = append(evs, dinv.Event{PR: r.PR, Kind: dinv.KindBriefGaveUp, Key: ts(st.GaveUpAt), At: st.GaveUpAt, Data: st.GaveUpReason})
		}
		if err := insertEvents(ctx, tx, evs); err != nil {
			return err
		}
		return nil
	})
	return err
}

// carryBrief fills a reused run with the brief, tags and model of the run
// it reuses, which must be the same pull request's.
func carryBrief(ctx context.Context, tx *sqlite.Tx, r *dinv.BriefRun) error {
	var tags string
	err := tx.QueryRowContext(ctx, `SELECT brief, tags, model FROM brief_runs WHERE id = ? AND owner = ? AND repo = ? AND number = ? AND brief != ''`,
		r.ReusedFrom, r.PR.Owner, r.PR.Repo, r.PR.Number).Scan(&r.Brief, &tags, &r.Model)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%s has no brief in run %d to reuse", r.PR, r.ReusedFrom)
	}
	if err != nil {
		return err
	}
	r.Tags, err = splitList(tags)
	return err
}

func replaceTags(ctx context.Context, tx *sqlite.Tx, ref github.Ref, run int64, tags []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM pr_tags WHERE owner = ? AND repo = ? AND number = ?`, ref.Owner, ref.Repo, ref.Number); err != nil {
		return err
	}
	for _, t := range tags {
		key, value, ok := strings.Cut(t, ":")
		if !ok || key == "" || value == "" {
			return fmt.Errorf("the tag %q of %s is not key:value", t, ref)
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO pr_tags (owner, repo, number, key, value, run) VALUES (?, ?, ?, ?, ?, ?)`,
			ref.Owner, ref.Repo, ref.Number, key, value, run); err != nil {
			return err
		}
	}
	return nil
}

// insertEvents stores events the loop writes, each at most once.
func insertEvents(ctx context.Context, tx *sqlite.Tx, evs []dinv.Event) error {
	for _, e := range evs {
		res, err := tx.ExecContext(ctx, insertEventSQL, eventArgs(e)...)
		if err := touchChanged(tx, res, err, port.TopicLoopEvents, e.PR); err != nil {
			return err
		}
	}
	return nil
}

// InterruptBriefRuns ends the runs still running, which a daemon that
// stopped mid-call left behind.
func (s *Store) InterruptBriefRuns(ctx context.Context, at time.Time) (int, error) {
	var evs []dinv.Event
	_, err := s.db.Write(ctx, func(tx *sqlite.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id, owner, repo, number FROM brief_runs WHERE outcome = ? ORDER BY id`, outcomeRunning)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id int64
			var ref github.Ref
			if err := rows.Scan(&id, &ref.Owner, &ref.Repo, &ref.Number); err != nil {
				rows.Close()
				return err
			}
			evs = append(evs, dinv.Event{PR: ref, Kind: dinv.KindBriefEnded, Key: strconv.FormatInt(id, 10), At: at, Data: outcomeInterrupted})
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE brief_runs SET outcome = ?, ended_at = ? WHERE outcome = ?`, outcomeInterrupted, ts(at), outcomeRunning); err != nil {
			return err
		}
		for _, e := range evs {
			tx.Touch(port.TopicBriefs, e.PR.Unit())
		}
		return insertEvents(ctx, tx, evs)
	})
	if err != nil {
		return 0, err
	}
	return len(evs), nil
}

// WantBrief records when a turn first wanted the pull request's brief; a
// later want keeps the first time.
func (s *Store) WantBrief(ctx context.Context, ref github.Ref, at time.Time) error {
	_, err := s.db.Write(ctx, func(tx *sqlite.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO pr_briefs (owner, repo, number, wanted_at) VALUES (?, ?, ?, ?)
			ON CONFLICT (owner, repo, number) DO UPDATE SET wanted_at = excluded.wanted_at WHERE wanted_at = ''`,
			ref.Owner, ref.Repo, ref.Number, ts(at))
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 1 {
			tx.Touch(port.TopicBriefs, ref.Unit())
			if err := insertEvents(ctx, tx, []dinv.Event{{PR: ref, Kind: dinv.KindBriefWanted, Key: ts(at), At: at}}); err != nil {
				return err
			}
		}
		return nil
	})
	return err
}

func (s *Store) ClearWanted(ctx context.Context, ref github.Ref) error {
	return s.exec(ctx, port.TopicBriefs, ref, `UPDATE pr_briefs SET wanted_at = '' WHERE owner = ? AND repo = ? AND number = ? AND wanted_at IS NOT ''`, ref.Owner, ref.Repo, ref.Number)
}

// RefreshBrief puts the pull request back to having no brief, no tags and
// no give-up; every run stays.
func (s *Store) RefreshBrief(ctx context.Context, ref github.Ref) error {
	_, err := s.db.Write(ctx, func(tx *sqlite.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE pr_briefs SET run = 0, failures = 0, gave_up_at = '', gave_up_reason = '' WHERE owner = ? AND repo = ? AND number = ?
			AND (run IS NOT 0 OR failures IS NOT 0 OR gave_up_at IS NOT '' OR gave_up_reason IS NOT '')`, ref.Owner, ref.Repo, ref.Number)
		if err := touchChanged(tx, res, err, port.TopicBriefs, ref); err != nil {
			return err
		}
		res, err = tx.ExecContext(ctx, `DELETE FROM pr_tags WHERE owner = ? AND repo = ? AND number = ?`, ref.Owner, ref.Repo, ref.Number)
		return touchChanged(tx, res, err, port.TopicBriefs, ref)
	})
	return err
}

// Tags lists the pull request's key:value tags, sorted.
func (s *Store) Tags(ctx context.Context, ref github.Ref) ([]string, error) {
	rows, err := s.db.R().QueryContext(ctx, `SELECT key || ':' || value FROM pr_tags WHERE owner = ? AND repo = ? AND number = ? ORDER BY key, value`,
		ref.Owner, ref.Repo, ref.Number)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func tagVocabularySQL(keys int) string {
	return `SELECT key, value, total FROM (
		SELECT key, value, total, ROW_NUMBER() OVER (PARTITION BY key ORDER BY own DESC, total DESC, value) AS n FROM (
			SELECT key, value, COUNT(*) AS total, SUM(owner = ?) AS own FROM pr_tags
			WHERE key IN (?` + strings.Repeat(", ?", keys-1) + `) GROUP BY key, value))
		WHERE n <= ? ORDER BY key, n`
}

// TagVocabulary lists, for each of keys, up to limit values of pr_tags with
// the number of pull requests that carry them: the ones owner's pull
// requests use first, then the most used, then by name.
func (s *Store) TagVocabulary(ctx context.Context, owner string, keys []string, limit int) (map[string][]tag.Count, error) {
	out := map[string][]tag.Count{}
	if len(keys) == 0 || limit <= 0 {
		return out, nil
	}
	args := []any{owner}
	for _, k := range keys {
		args = append(args, k)
	}
	args = append(args, limit)
	rows, err := s.db.R().QueryContext(ctx, tagVocabularySQL(len(keys)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var c tag.Count
		if err := rows.Scan(&key, &c.Value, &c.Count); err != nil {
			return nil, err
		}
		out[key] = append(out[key], c)
	}
	return out, rows.Err()
}

const briefRunColumns = `id, owner, repo, number, trigger, intake, version, attempt_of, head_oid, body_edited_at, changed_files, started_at, ended_at,
	input_ms, call_ms, agent_ms, outcome, error, model, cost_usd, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
	diff_bytes, diff_cut, brief, files, tags, dropped_tags, input_sha, reused_from`

// briefRunSummaryColumns is briefRunColumns with the brief and the files left
// empty, so a list does not read the largest columns.
const briefRunSummaryColumns = `id, owner, repo, number, trigger, intake, version, attempt_of, head_oid, body_edited_at, changed_files, started_at, ended_at,
	input_ms, call_ms, agent_ms, outcome, error, model, cost_usd, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
	diff_bytes, diff_cut, '', '', tags, dropped_tags, input_sha, reused_from`

// BriefRuns lists the runs started at or after since, oldest first, without
// their brief and files; BriefRun reads them whole.
func (s *Store) BriefRuns(ctx context.Context, since time.Time) ([]dinv.BriefRun, error) {
	rows, err := s.db.R().QueryContext(ctx, `SELECT `+briefRunSummaryColumns+` FROM brief_runs WHERE started_at >= ? ORDER BY id`, ts(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dinv.BriefRun
	for rows.Next() {
		r, err := scanBriefRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) BriefRun(ctx context.Context, id int64) (dinv.BriefRun, bool, error) {
	r, err := scanBriefRun(s.db.R().QueryRowContext(ctx, `SELECT `+briefRunColumns+` FROM brief_runs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return dinv.BriefRun{}, false, nil
	}
	return r, err == nil, err
}

func scanBriefRun(row scanner) (dinv.BriefRun, error) {
	var r dinv.BriefRun
	var edited, started, ended, files, tags, dropped string
	var inMS, callMS, agentMS sql.Null[int64]
	var in, out, read, write sql.Null[int64]
	var cost sql.Null[float64]
	var diff sql.Null[int]
	if err := row.Scan(&r.ID, &r.PR.Owner, &r.PR.Repo, &r.PR.Number, &r.Trigger, &r.Intake, &r.Version, &r.AttemptOf, &r.HeadOid, &edited, &r.ChangedFiles,
		&started, &ended, &inMS, &callMS, &agentMS, &r.Outcome, &r.Error, &r.Model, &cost, &in, &out, &read, &write,
		&diff, &r.DiffCut, &r.Brief, &files, &tags, &dropped, &r.InputSHA, &r.ReusedFrom); err != nil {
		return dinv.BriefRun{}, err
	}
	r.InputMS, r.CallMS, r.AgentMS, r.CostUSD, r.DiffBytes = pointer(inMS), pointer(callMS), pointer(agentMS), pointer(cost), pointer(diff)
	if in.Valid || out.Valid || read.Valid || write.Valid {
		r.Tokens = &usage.Tokens{Input: in.V, Output: out.V, CacheRead: read.V, CacheWrite: write.V}
	}
	var err error
	for _, t := range []struct {
		dst *time.Time
		src string
	}{{&r.BodyEditedAt, edited}, {&r.StartedAt, started}, {&r.EndedAt, ended}} {
		if *t.dst, err = parseTS(t.src); err != nil {
			return dinv.BriefRun{}, err
		}
	}
	for _, l := range []struct {
		dst *[]string
		src string
	}{{&r.Files, files}, {&r.Tags, tags}, {&r.DroppedTags, dropped}} {
		if *l.dst, err = splitList(l.src); err != nil {
			return dinv.BriefRun{}, fmt.Errorf("brief run %d: %w", r.ID, err)
		}
	}
	return r, nil
}

// briefRecordQuery reads the brief in use from the run and the error and
// attempt time from the last run; a last run that did not end ok is a
// failure even without an error text.
var briefRecordQuery = `SELECT p.owner, p.repo, p.number, ` + fmt.Sprintf(briefFields, "COALESCE(r.brief, '')") + `
	FROM pr_briefs p LEFT JOIN brief_runs r ON p.run != 0 AND r.id = p.run LEFT JOIN brief_runs l ON p.last_run != 0 AND l.id = p.last_run`

// briefFields are the columns of a BriefRecord after its pull request, read
// from pr_briefs p, the run in use r and the last run l; the brief JSON
// is the one column a caller may swap for an empty string.
const briefFields = `COALESCE(p.failures, 0), COALESCE(p.attempt_of, ''), COALESCE(p.run, 0), COALESCE(p.last_run, 0), COALESCE(p.wanted_at, ''), COALESCE(p.retry_at, ''),
	COALESCE(p.gave_up_at, ''), COALESCE(p.gave_up_reason, ''),
	COALESCE(r.version, ''), COALESCE(r.head_oid, ''), COALESCE(r.model, ''), COALESCE(r.body_edited_at, ''), COALESCE(r.changed_files, 0),
	%s, COALESCE(r.ended_at, ''), COALESCE(r.cost_usd, 0),
	CASE WHEN l.outcome IS NULL OR l.outcome IN ('ok', 'reused', 'running') THEN '' ELSE COALESCE(NULLIF(l.error, ''), l.outcome) END, COALESCE(l.started_at, ''),
	COALESCE(r.input_sha, '')`

type briefTimes struct{ wanted, retry, gaveUp, edited, created, attempted string }

// dests are the scan targets of briefFields.
func (x *briefTimes) dests(b *dinv.BriefRecord) []any {
	return []any{&b.Failures, &b.AttemptOf, &b.Run, &b.LastRun, &x.wanted, &x.retry, &x.gaveUp, &b.GaveUpReason,
		&b.Version, &b.HeadOid, &b.Model, &x.edited, &b.ChangedFiles, &b.Brief, &x.created, &b.CostUSD, &b.Error, &x.attempted, &b.InputSHA}
}

func (x *briefTimes) parse(b *dinv.BriefRecord) error {
	for _, t := range []struct {
		dst *time.Time
		src string
	}{{&b.WantedAt, x.wanted}, {&b.RetryAt, x.retry}, {&b.GaveUpAt, x.gaveUp}, {&b.BodyEditedAt, x.edited}, {&b.CreatedAt, x.created}, {&b.AttemptedAt, x.attempted}} {
		var err error
		if *t.dst, err = parseTS(t.src); err != nil {
			return err
		}
	}
	return nil
}

func scanBrief(row scanner) (dinv.BriefRecord, error) {
	var b dinv.BriefRecord
	var x briefTimes
	if err := row.Scan(append([]any{&b.PR.Owner, &b.PR.Repo, &b.PR.Number}, x.dests(&b)...)...); err != nil {
		return dinv.BriefRecord{}, err
	}
	if err := x.parse(&b); err != nil {
		return dinv.BriefRecord{}, err
	}
	return b, nil
}

func (s *Store) BriefRecord(ctx context.Context, ref github.Ref) (dinv.BriefRecord, bool, error) {
	b, err := scanBrief(s.db.R().QueryRowContext(ctx, briefRecordQuery+` WHERE p.owner = ? AND p.repo = ? AND p.number = ?`, ref.Owner, ref.Repo, ref.Number))
	if errors.Is(err, sql.ErrNoRows) {
		return dinv.BriefRecord{}, false, nil
	}
	return b, err == nil, err
}

// BriefRecords reads the brief records of refs, of every pull request when
// refs is empty.
func (s *Store) BriefRecords(ctx context.Context, refs ...github.Ref) (map[github.Ref]dinv.BriefRecord, error) {
	out := map[github.Ref]dinv.BriefRecord{}
	if len(refs) == 0 {
		return out, s.readBriefRecords(ctx, out, briefRecordQuery)
	}
	for chunk := range slices.Chunk(refs, prReadChunk) {
		marks := strings.TrimSuffix(strings.Repeat("(?,?,?),", len(chunk)), ",")
		args := make([]any, 0, 3*len(chunk))
		for _, r := range chunk {
			args = append(args, r.Owner, r.Repo, r.Number)
		}
		if err := s.readBriefRecords(ctx, out, briefRecordQuery+` WHERE (p.owner, p.repo, p.number) IN (VALUES `+marks+`)`, args...); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) readBriefRecords(ctx context.Context, out map[github.Ref]dinv.BriefRecord, q string, args ...any) error {
	rows, err := s.db.R().QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		b, err := scanBrief(rows)
		if err != nil {
			return err
		}
		out[b.PR] = b
	}
	return rows.Err()
}

// BriefState is an open pull request with the state of its brief, Rec.Brief
// left empty; Has is false for a pull request with no pr_briefs row.
type BriefState struct {
	PR  dinv.PR
	Rec dinv.BriefRecord
	Has bool
}

type joined struct {
	scanner
	more []any
}

func (j joined) Scan(dest ...any) error { return j.scanner.Scan(append(dest, j.more...)...) }

// BriefStates lists the open pull requests with their brief state, in one
// query that never reads a brief's JSON, newest update first.
func (s *Store) BriefStates(ctx context.Context) ([]BriefState, error) {
	rows, err := s.db.R().QueryContext(ctx, `SELECT `+prColumns+`, p.owner IS NOT NULL, `+fmt.Sprintf(briefFields, "''")+`
		FROM pull_requests pr LEFT JOIN pr_briefs p ON p.owner = pr.owner AND p.repo = pr.repo AND p.number = pr.number
		LEFT JOIN brief_runs r ON p.run != 0 AND r.id = p.run LEFT JOIN brief_runs l ON p.last_run != 0 AND l.id = p.last_run
		WHERE pr.state = 'OPEN' ORDER BY pr.updated_at DESC, pr.owner, pr.repo, pr.number DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BriefState
	for rows.Next() {
		var st BriefState
		var x briefTimes
		pr, err := scanPR(joined{rows, append([]any{&st.Has}, x.dests(&st.Rec)...)})
		if err != nil {
			return nil, err
		}
		if err := x.parse(&st.Rec); err != nil {
			return nil, err
		}
		st.PR, st.Rec.PR = pr, pr.Ref
		if !st.Has {
			st.Rec = dinv.BriefRecord{PR: pr.Ref}
		}
		out = append(out, st)
	}
	return out, rows.Err()
}
