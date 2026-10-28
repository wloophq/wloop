package inventory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/port"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/tag"
	"github.com/wloophq/wloop/internal/domain/unit"
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

// githubBriefs is the provider of the briefs this store reads and writes by
// pull request.
const githubBriefs = "github"

// briefIntent is the intent of the runs the brief queue writes.
const briefIntent = "brief"

// briefRevision is a pull request's head and body edit time, as a run
// stores what the pull request was at.
func briefRevision(head string, edited time.Time) string { return head + " " + ts(edited) }

func parseBriefRevision(rev string) (head string, edited time.Time, err error) {
	head, at, _ := strings.Cut(rev, " ")
	edited, err = parseTS(at)
	return head, edited, err
}

// briefInput is the GitHub parts of what a run read, stored as its input.
type briefInput struct {
	ChangedFiles int      `json:"changed_files"`
	Files        []string `json:"files,omitempty"`
}

func briefInputJSON(changed int, files []string) string {
	b, _ := json.Marshal(briefInput{ChangedFiles: changed, Files: files})
	return string(b)
}

func parseBriefInput(raw string) (briefInput, error) {
	var in briefInput
	if raw == "" {
		return in, nil
	}
	err := json.Unmarshal([]byte(raw), &in)
	return in, err
}

// StartBriefRun stores r as running and returns its id.
func (s *Store) StartBriefRun(ctx context.Context, r dinv.BriefRun) (int64, error) {
	var id int64
	_, err := s.db.Write(ctx, func(tx *sqlite.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO brief_runs (provider, unit_key, intent, trigger, intake, version, attempt_of, revision, input, started_at, outcome)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			githubBriefs, r.PR.String(), briefIntent, r.Trigger, r.Intake, r.Version, r.AttemptOf, briefRevision(r.HeadOid, r.BodyEditedAt),
			briefInputJSON(r.ChangedFiles, nil), ts(r.StartedAt), outcomeRunning)
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

// EndBriefRun updates r, sets unit_briefs.last_run (and run, failures cleared, on ok or reused; a reused run carries the brief, tags and model of ReusedFrom) and the state fields of st, and for an ok run replaces unit_tags with r.Tags, in one transaction with KindBriefEnded and, when st newly gives up, KindBriefGaveUp.
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
		res, err := tx.ExecContext(ctx, `UPDATE brief_runs SET trigger = ?, intake = ?, version = ?, attempt_of = ?, revision = ?, input = ?,
			started_at = ?, ended_at = ?, input_ms = ?, call_ms = ?, agent_ms = ?, outcome = ?, error = ?, model = ?, cost_usd = ?,
			input_tokens = ?, output_tokens = ?, cache_read_tokens = ?, cache_write_tokens = ?, input_bytes = ?, input_cut = ?,
			brief = ?, tags = ?, dropped_tags = ?, input_sha = ?, reused_from = ?
			WHERE id = ? AND provider = ? AND unit_key = ?`,
			r.Trigger, r.Intake, r.Version, r.AttemptOf, briefRevision(r.HeadOid, r.BodyEditedAt), briefInputJSON(r.ChangedFiles, r.Files),
			ts(r.StartedAt), ts(r.EndedAt), nullable(r.InputMS), nullable(r.CallMS), nullable(r.AgentMS), r.Outcome, r.Error, r.Model, nullable(r.CostUSD),
			nullable(in), nullable(out), nullable(read), nullable(write), nullable(r.DiffBytes), r.DiffCut,
			r.Brief, joinList(r.Tags), joinList(r.DroppedTags), r.InputSHA, r.ReusedFrom,
			r.ID, githubBriefs, r.PR.String())
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
		err = tx.QueryRowContext(ctx, `SELECT gave_up_at FROM unit_briefs WHERE provider = ? AND unit_key = ?`, githubBriefs, r.PR.String()).Scan(&gaveUp)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		ok := r.Outcome == outcomeOK || r.Outcome == outcomeReused
		failures := st.Failures
		if ok {
			failures = 0
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO unit_briefs (provider, unit_key, failures, attempt_of, run, last_run, wanted_at, retry_at, gave_up_at, gave_up_reason)
			VALUES (?1, ?2, ?3, ?4, iif(?5, ?6, 0), ?6, ?7, ?8, ?9, ?10)
			ON CONFLICT (provider, unit_key) DO UPDATE SET failures = excluded.failures, attempt_of = excluded.attempt_of,
			run = iif(?5, ?6, run), last_run = excluded.last_run, wanted_at = excluded.wanted_at, retry_at = excluded.retry_at,
			gave_up_at = excluded.gave_up_at, gave_up_reason = excluded.gave_up_reason`,
			githubBriefs, r.PR.String(), failures, st.AttemptOf, ok, r.ID, ts(st.WantedAt), ts(st.RetryAt), ts(st.GaveUpAt), st.GaveUpReason); err != nil {
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
	err := tx.QueryRowContext(ctx, `SELECT brief, tags, model FROM brief_runs WHERE id = ? AND provider = ? AND unit_key = ? AND brief != ''`,
		r.ReusedFrom, githubBriefs, r.PR.String()).Scan(&r.Brief, &tags, &r.Model)
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
	if _, err := tx.ExecContext(ctx, `DELETE FROM unit_tags WHERE provider = ? AND unit_key = ?`, githubBriefs, ref.String()); err != nil {
		return err
	}
	for _, t := range tags {
		key, value, ok := strings.Cut(t, ":")
		if !ok || key == "" || value == "" {
			return fmt.Errorf("the tag %q of %s is not key:value", t, ref)
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO unit_tags (provider, unit_key, key, value, run) VALUES (?, ?, ?, ?, ?)`,
			githubBriefs, ref.String(), key, value, run); err != nil {
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
		rows, err := tx.QueryContext(ctx, `SELECT id, unit_key FROM brief_runs WHERE outcome = ? AND provider = ? ORDER BY id`, outcomeRunning, githubBriefs)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id int64
			var key string
			if err := rows.Scan(&id, &key); err != nil {
				rows.Close()
				return err
			}
			ref, err := pr.ParseKey(key)
			if err != nil {
				rows.Close()
				return fmt.Errorf("brief run %d: %w", id, err)
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
		res, err := tx.ExecContext(ctx, `INSERT INTO unit_briefs (provider, unit_key, wanted_at) VALUES (?, ?, ?)
			ON CONFLICT (provider, unit_key) DO UPDATE SET wanted_at = excluded.wanted_at WHERE wanted_at = ''`,
			githubBriefs, ref.String(), ts(at))
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
	return s.exec(ctx, port.TopicBriefs, ref, `UPDATE unit_briefs SET wanted_at = '' WHERE provider = ? AND unit_key = ? AND wanted_at IS NOT ''`, githubBriefs, ref.String())
}

// RefreshBrief puts the pull request back to having no brief, no tags and
// no give-up; every run stays.
func (s *Store) RefreshBrief(ctx context.Context, ref github.Ref) error {
	_, err := s.db.Write(ctx, func(tx *sqlite.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE unit_briefs SET run = 0, failures = 0, gave_up_at = '', gave_up_reason = '' WHERE provider = ? AND unit_key = ?
			AND (run IS NOT 0 OR failures IS NOT 0 OR gave_up_at IS NOT '' OR gave_up_reason IS NOT '')`, githubBriefs, ref.String())
		if err := touchChanged(tx, res, err, port.TopicBriefs, ref); err != nil {
			return err
		}
		res, err = tx.ExecContext(ctx, `DELETE FROM unit_tags WHERE provider = ? AND unit_key = ?`, githubBriefs, ref.String())
		return touchChanged(tx, res, err, port.TopicBriefs, ref)
	})
	return err
}

// Tags lists the pull request's key:value tags, sorted.
func (s *Store) Tags(ctx context.Context, ref github.Ref) ([]string, error) {
	rows, err := s.db.R().QueryContext(ctx, `SELECT key || ':' || value FROM unit_tags WHERE provider = ? AND unit_key = ? ORDER BY key, value`,
		githubBriefs, ref.String())
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
			SELECT key, value, COUNT(*) AS total, SUM(substr(unit_key, 1, instr(unit_key, '/') - 1) = ?) AS own FROM unit_tags
			WHERE key IN (?` + strings.Repeat(", ?", keys-1) + `) AND +provider = '` + githubBriefs + `' GROUP BY key, value))
		WHERE n <= ? ORDER BY key, n`
}

// TagVocabulary lists, for each of keys, up to limit values of the github unit_tags with
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

const briefRunColumns = `id, unit_key, trigger, intake, version, attempt_of, revision, input, started_at, ended_at,
	input_ms, call_ms, agent_ms, outcome, error, model, cost_usd, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
	input_bytes, input_cut, brief, tags, dropped_tags, input_sha, reused_from`

// briefRunSummaryColumns is briefRunColumns with the brief and the files left
// empty, so a list does not read the largest columns.
const briefRunSummaryColumns = `id, unit_key, trigger, intake, version, attempt_of, revision, json_remove(input, '$.files'), started_at, ended_at,
	input_ms, call_ms, agent_ms, outcome, error, model, cost_usd, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
	input_bytes, input_cut, '', tags, dropped_tags, input_sha, reused_from`

// BriefRuns lists the runs started at or after since, oldest first, without
// their brief and files; BriefRun reads them whole.
func (s *Store) BriefRuns(ctx context.Context, since time.Time) ([]dinv.BriefRun, error) {
	rows, err := s.db.R().QueryContext(ctx, `SELECT `+briefRunSummaryColumns+` FROM brief_runs WHERE started_at >= ? AND provider = ? ORDER BY id`, ts(since), githubBriefs)
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
	r, err := scanBriefRun(s.db.R().QueryRowContext(ctx, `SELECT `+briefRunColumns+` FROM brief_runs WHERE id = ? AND provider = ?`, id, githubBriefs))
	if errors.Is(err, sql.ErrNoRows) {
		return dinv.BriefRun{}, false, nil
	}
	return r, err == nil, err
}

func scanBriefRun(row scanner) (dinv.BriefRun, error) {
	var r dinv.BriefRun
	var key, revision, input, started, ended, tags, dropped string
	var inMS, callMS, agentMS sql.Null[int64]
	var in, out, read, write sql.Null[int64]
	var cost sql.Null[float64]
	var diff sql.Null[int]
	if err := row.Scan(&r.ID, &key, &r.Trigger, &r.Intake, &r.Version, &r.AttemptOf, &revision, &input,
		&started, &ended, &inMS, &callMS, &agentMS, &r.Outcome, &r.Error, &r.Model, &cost, &in, &out, &read, &write,
		&diff, &r.DiffCut, &r.Brief, &tags, &dropped, &r.InputSHA, &r.ReusedFrom); err != nil {
		return dinv.BriefRun{}, err
	}
	var err error
	if r.PR, err = pr.ParseKey(key); err != nil {
		return dinv.BriefRun{}, fmt.Errorf("brief run %d: %w", r.ID, err)
	}
	if r.HeadOid, r.BodyEditedAt, err = parseBriefRevision(revision); err != nil {
		return dinv.BriefRun{}, fmt.Errorf("brief run %d: %w", r.ID, err)
	}
	parts, err := parseBriefInput(input)
	if err != nil {
		return dinv.BriefRun{}, fmt.Errorf("brief run %d: %w", r.ID, err)
	}
	r.ChangedFiles, r.Files = parts.ChangedFiles, parts.Files
	r.InputMS, r.CallMS, r.AgentMS, r.CostUSD, r.DiffBytes = pointer(inMS), pointer(callMS), pointer(agentMS), pointer(cost), pointer(diff)
	if in.Valid || out.Valid || read.Valid || write.Valid {
		r.Tokens = &usage.Tokens{Input: in.V, Output: out.V, CacheRead: read.V, CacheWrite: write.V}
	}
	for _, t := range []struct {
		dst *time.Time
		src string
	}{{&r.StartedAt, started}, {&r.EndedAt, ended}} {
		if *t.dst, err = parseTS(t.src); err != nil {
			return dinv.BriefRun{}, err
		}
	}
	for _, l := range []struct {
		dst *[]string
		src string
	}{{&r.Tags, tags}, {&r.DroppedTags, dropped}} {
		if *l.dst, err = splitList(l.src); err != nil {
			return dinv.BriefRun{}, fmt.Errorf("brief run %d: %w", r.ID, err)
		}
	}
	return r, nil
}

// briefRecordQuery reads the brief in use from the run and the error and
// attempt time from the last run; a last run that did not end ok is a
// failure even without an error text.
var briefRecordQuery = `SELECT p.unit_key, ` + fmt.Sprintf(briefFields, "COALESCE(r.brief, '')") + `
	FROM unit_briefs p LEFT JOIN brief_runs r ON p.run != 0 AND r.id = p.run LEFT JOIN brief_runs l ON p.last_run != 0 AND l.id = p.last_run
	WHERE p.provider = '` + githubBriefs + `'`

// briefFields are the columns of a BriefRecord after its pull request, read
// from unit_briefs p, the run in use r and the last run l; the brief JSON
// is the one column a caller may swap for an empty string.
const briefFields = `COALESCE(p.failures, 0), COALESCE(p.attempt_of, ''), COALESCE(p.run, 0), COALESCE(p.last_run, 0), COALESCE(p.wanted_at, ''), COALESCE(p.retry_at, ''),
	COALESCE(p.gave_up_at, ''), COALESCE(p.gave_up_reason, ''),
	COALESCE(r.version, ''), COALESCE(r.revision, ''), COALESCE(r.model, ''), COALESCE(json_extract(r.input, '$.changed_files'), 0),
	%s, COALESCE(r.ended_at, ''), COALESCE(r.cost_usd, 0),
	CASE WHEN l.outcome IS NULL OR l.outcome IN ('ok', 'reused', 'running') THEN '' ELSE COALESCE(NULLIF(l.error, ''), l.outcome) END, COALESCE(l.started_at, ''),
	COALESCE(r.input_sha, '')`

type briefTimes struct{ wanted, retry, gaveUp, revision, created, attempted string }

// dests are the scan targets of briefFields.
func (x *briefTimes) dests(b *dinv.BriefRecord) []any {
	return []any{&b.Failures, &b.AttemptOf, &b.Run, &b.LastRun, &x.wanted, &x.retry, &x.gaveUp, &b.GaveUpReason,
		&b.Version, &x.revision, &b.Model, &b.ChangedFiles, &b.Brief, &x.created, &b.CostUSD, &b.Error, &x.attempted, &b.InputSHA}
}

func (x *briefTimes) parse(b *dinv.BriefRecord) error {
	if x.revision != "" {
		var err error
		if b.HeadOid, b.BodyEditedAt, err = parseBriefRevision(x.revision); err != nil {
			return err
		}
	}
	for _, t := range []struct {
		dst *time.Time
		src string
	}{{&b.WantedAt, x.wanted}, {&b.RetryAt, x.retry}, {&b.GaveUpAt, x.gaveUp}, {&b.CreatedAt, x.created}, {&b.AttemptedAt, x.attempted}} {
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
	var key string
	if err := row.Scan(append([]any{&key}, x.dests(&b)...)...); err != nil {
		return dinv.BriefRecord{}, err
	}
	var err error
	if b.PR, err = pr.ParseKey(key); err != nil {
		return dinv.BriefRecord{}, err
	}
	if err := x.parse(&b); err != nil {
		return dinv.BriefRecord{}, err
	}
	return b, nil
}

func (s *Store) BriefRecord(ctx context.Context, ref github.Ref) (dinv.BriefRecord, bool, error) {
	b, err := scanBrief(s.db.R().QueryRowContext(ctx, briefRecordQuery+` AND p.unit_key = ?`, ref.String()))
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
		marks := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		args := make([]any, 0, len(chunk))
		for _, r := range chunk {
			args = append(args, r.String())
		}
		if err := s.readBriefRecords(ctx, out, briefRecordQuery+` AND p.unit_key IN (`+marks+`)`, args...); err != nil {
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
// left empty; Has is false for a pull request with no unit_briefs row.
type BriefState struct {
	PR  dinv.PR
	Rec dinv.BriefRecord
	Has bool
}

// briefStateQuery reads the brief state of the pull requests whose unit
// keys it is bound to, Brief left empty.
var briefStateQuery = `SELECT p.unit_key, ` + fmt.Sprintf(briefFields, "''") + `
	FROM unit_briefs p LEFT JOIN brief_runs r ON p.run != 0 AND r.id = p.run LEFT JOIN brief_runs l ON p.last_run != 0 AND l.id = p.last_run
	WHERE p.provider = '` + githubBriefs + `' AND p.unit_key IN (SELECT value FROM json_each(?))`

// BriefStates lists the open pull requests with their brief state, newest
// update first: the pull requests, then the brief states, a read each that
// never reads a brief's JSON.
func (s *Store) BriefStates(ctx context.Context) ([]BriefState, error) {
	prs, err := s.ListPRs(ctx, true)
	if err != nil {
		return nil, err
	}
	keys := make([]string, len(prs))
	for i, p := range prs {
		keys[i] = p.Ref.String()
	}
	list, err := json.Marshal(keys)
	if err != nil {
		return nil, err
	}
	recs := map[github.Ref]dinv.BriefRecord{}
	if err := s.readBriefRecords(ctx, recs, briefStateQuery, string(list)); err != nil {
		return nil, err
	}
	out := make([]BriefState, len(prs))
	for i, p := range prs {
		rec, has := recs[p.Ref]
		if !has {
			rec = dinv.BriefRecord{PR: p.Ref}
		}
		out[i] = BriefState{PR: p, Rec: rec, Has: has}
	}
	return out, nil
}

// UnitBrief is the brief in use of a unit of any provider: its run, its
// prompt version and its JSON, false when it has none.
type UnitBrief struct {
	Run     int64
	Version string
	Brief   string
}

const unitBriefSQL = `SELECT b.run, r.version, r.brief FROM unit_briefs b JOIN brief_runs r ON b.run != 0 AND r.id = b.run
	WHERE b.provider = ? AND b.unit_key = ? AND r.brief != ''`

// UnitBrief reads u's brief in use.
func (s *Store) UnitBrief(ctx context.Context, u unit.Unit) (UnitBrief, bool, error) {
	var b UnitBrief
	err := s.db.R().QueryRowContext(ctx, unitBriefSQL, string(u.Provider), u.Key).Scan(&b.Run, &b.Version, &b.Brief)
	if errors.Is(err, sql.ErrNoRows) {
		return UnitBrief{}, false, nil
	}
	return b, err == nil, err
}
