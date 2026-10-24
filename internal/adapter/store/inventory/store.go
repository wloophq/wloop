// Package inventory stores the pull requests in scope, their event history
// and the user's overrides in SQLite.
package inventory

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/event"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
)

const timeLayout = event.TimeLayout

const eventsChunk = 100

const prReadChunk = 400

const seeChunk = 500

type Store struct {
	db    *sqlite.DB
	clock func() time.Time
}

func New(db *sqlite.DB) *Store { return &Store{db: db, clock: time.Now} }

// exec runs one statement in its own write transaction and touches t for
// ref when it changed a row.
func (s *Store) exec(ctx context.Context, t port.Topic, ref github.Ref, query string, args ...any) error {
	_, err := s.db.Write(ctx, func(tx *sqlite.Tx) error {
		res, err := tx.ExecContext(ctx, query, args...)
		return touchChanged(tx, res, err, t, ref)
	})
	return err
}

// touchChanged touches t for ref when the statement that returned res and
// err changed a row.
func touchChanged(tx *sqlite.Tx, res sql.Result, err error, t port.Topic, ref github.Ref) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n > 0 {
		tx.Touch(t, ref.Unit())
	}
	return nil
}

// UseClock dates the events the store writes on its own, such as followed,
// by now.
func (s *Store) UseClock(now func() time.Time) { s.clock = now }

func ts(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(timeLayout)
}

func parseTS(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(timeLayout, s)
}

func joinList(v []string) string {
	if len(v) == 0 {
		return ""
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func splitList(s string) ([]string, error) {
	if s == "" {
		return nil, nil
	}
	var v []string
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil, err
	}
	return v, nil
}

func joinStates(states []turn.AgentState) string {
	raw := make([]string, 0, len(states))
	for _, s := range states {
		raw = append(raw, s.String())
	}
	return joinList(raw)
}

func splitStates(s string) ([]turn.AgentState, error) {
	raw, err := splitList(s)
	if err != nil || raw == nil {
		return nil, err
	}
	out := make([]turn.AgentState, 0, len(raw))
	for _, r := range raw {
		out = append(out, turn.ParseAgentState(r))
	}
	return out, nil
}

const upsertPRSQL = `INSERT INTO pull_requests
 (owner, repo, number, title, author, state, is_draft, head_oid, head_branch, base_branch, is_fork, checks_state, review_decision, review_requests, url, created_at, updated_at, merged_at, closed_at, reasons, body_edited_at, changed_files, failing_checks, mergeable, mergeable_at)
 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT(owner, repo, number) DO UPDATE SET title=excluded.title, author=excluded.author, state=excluded.state,
 is_draft=excluded.is_draft, head_oid=excluded.head_oid, head_branch=excluded.head_branch, base_branch=excluded.base_branch,
 is_fork=excluded.is_fork, checks_state=excluded.checks_state, review_decision=excluded.review_decision, review_requests=excluded.review_requests,
 url=excluded.url, created_at=excluded.created_at, updated_at=excluded.updated_at, merged_at=excluded.merged_at, closed_at=excluded.closed_at,
 reasons=excluded.reasons, body_edited_at=excluded.body_edited_at, changed_files=excluded.changed_files, failing_checks=excluded.failing_checks, mergeable=excluded.mergeable, mergeable_at=excluded.mergeable_at
 WHERE (title, author, state, is_draft, head_oid, head_branch, base_branch, is_fork, checks_state, review_decision, review_requests, url, created_at, updated_at, merged_at, closed_at, reasons, body_edited_at, changed_files, failing_checks, mergeable, mergeable_at)
 IS NOT (excluded.title, excluded.author, excluded.state, excluded.is_draft, excluded.head_oid, excluded.head_branch, excluded.base_branch, excluded.is_fork, excluded.checks_state, excluded.review_decision, excluded.review_requests, excluded.url, excluded.created_at, excluded.updated_at, excluded.merged_at, excluded.closed_at, excluded.reasons, excluded.body_edited_at, excluded.changed_files, excluded.failing_checks, excluded.mergeable, excluded.mergeable_at)`

// UpsertPR stores one snapshot as SaveSnapshots does, seen at its LastSeenAt.
func (s *Store) UpsertPR(ctx context.Context, pr dinv.PR) error {
	_, err := s.SaveSnapshots(ctx, []Snapshot{{PR: pr}}, pr.LastSeenAt)
	return err
}

// StoredFacts is what storing a fresh GitHub snapshot needs of the stored one.
type StoredFacts struct {
	PR             dinv.PR // zero when not Found
	Found          bool
	Pushes         map[string]time.Time // push key -> at
	UnfilledMerges []time.Time          // merged events lacking the merger or the commit
}

func storedFactsEventsSQL(marks string) string {
	return `SELECT unit_key, kind, key, at FROM events
		WHERE provider = 'github' AND unit_key IN (` + marks + `) AND (kind = ? OR (kind = ? AND key = '' AND (actor = '' OR data = '')))`
}

// StoredFacts reads the facts of refs: the stored pull requests, then their
// pushes and unfilled merges, one query each per chunk of refs.
func (s *Store) StoredFacts(ctx context.Context, refs []github.Ref) (map[github.Ref]StoredFacts, error) {
	prs, err := s.PRs(ctx, refs)
	if err != nil {
		return nil, err
	}
	out := make(map[github.Ref]StoredFacts, len(refs))
	for _, r := range refs {
		p, found := prs[r]
		out[r] = StoredFacts{PR: p, Found: found}
	}
	for chunk := range slices.Chunk(refs, prReadChunk) {
		marks, args := keyValues(chunk)
		rows, err := s.db.R().QueryContext(ctx, storedFactsEventsSQL(marks), append(args, dinv.KindPush, dinv.KindMerged)...)
		if err != nil {
			return nil, err
		}
		err = func() error {
			defer rows.Close()
			for rows.Next() {
				var unitKey, kind, key, raw string
				if err := rows.Scan(&unitKey, &kind, &key, &raw); err != nil {
					return err
				}
				ref := refOf(string(unit.GitHub), unitKey)
				at, err := parseTS(raw)
				if err != nil {
					return err
				}
				f := out[ref]
				if kind == dinv.KindPush {
					if f.Pushes == nil {
						f.Pushes = map[string]time.Time{}
					}
					f.Pushes[key] = at
				} else {
					f.UnfilledMerges = append(f.UnfilledMerges, at)
				}
				out[ref] = f
			}
			return rows.Err()
		}()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// refValues is the VALUES rows and arguments of refs, for an IN on the
// primary key.
func refValues(refs []github.Ref) (string, []any) {
	args := make([]any, 0, 3*len(refs))
	for _, r := range refs {
		args = append(args, r.Owner, r.Repo, r.Number)
	}
	return strings.TrimSuffix(strings.Repeat("(?,?,?),", len(refs)), ","), args
}

// unitArgs are the provider and unit_key column values of ref; the zero ref
// is the zero unit.
func unitArgs(ref github.Ref) []any {
	u := ref.Unit()
	return []any{string(u.Provider), u.Key}
}

// keyValues is the placeholders and arguments of the unit keys of refs, for
// an IN on unit_key.
func keyValues(refs []github.Ref) (string, []any) {
	args := make([]any, 0, len(refs))
	for _, r := range refs {
		args = append(args, r.Unit().Key)
	}
	return strings.TrimSuffix(strings.Repeat("?,", len(refs)), ","), args
}

// refOf is the pull request of a unit's columns, the zero ref for a unit of
// another provider.
func refOf(provider, key string) github.Ref {
	if unit.ProviderID(provider) != unit.GitHub {
		return github.Ref{}
	}
	ref, _ := pr.ParseKey(key)
	return ref
}

// unitKeyOf is the SQL of the github unit key of the row aliased alias,
// which has owner, repo and number columns.
func unitKeyOf(alias string) string {
	return alias + `.owner || '/' || ` + alias + `.repo || '#' || ` + alias + `.number`
}

// prOfUnit is the SQL condition that the pull_requests row p is the pull
// request of the github unit key in column col, in a form the primary key
// serves.
func prOfUnit(col string) string {
	return strings.NewReplacer("K", col).Replace(`p.owner = substr(K, 1, instr(K, '/') - 1)
	 AND p.repo = substr(K, instr(K, '/') + 1, instr(K, '#') - instr(K, '/') - 1)
	 AND p.number = CAST(substr(K, instr(K, '#') + 1) AS INTEGER)`)
}

// Snapshot is one pull request as GitHub returned it, ready to store.
type Snapshot struct {
	PR     dinv.PR      // Reasons are merged with the stored ones; LastSeenAt is ignored
	Events []dinv.Event // the timeline; those already stored are ignored
	Merges []dinv.Event // merged events that fill a stored one lacking the merger or the commit
	Detail *Detail      // nil keeps the stored one
}

// stored is what saving a snapshot reads of the stored row inside the write
// transaction.
type stored struct {
	found                  bool
	reasons                string
	mergeable, mergeableAt string
	tracked                bool
	ws                     string
	scope                  scope
	followedAt             time.Time // the newest followed event
	detail                 *storedDetail
}

// SaveSnapshots stores snaps in one write transaction: a row is rewritten
// only when a column differs, every ref is recorded as seen, the
// events already stored are skipped, and a followed or unfollowed event is
// recorded for each pull request whose scope flipped. A detail is written
// only when it differs from the stored one, and noted only when its body
// does. seenAt dates the sighting, a new conflict, the flips and a detail
// without ReadAt. It returns the events added per ref that gained any.
func (s *Store) SaveSnapshots(ctx context.Context, snaps []Snapshot, seenAt time.Time) (map[github.Ref]int, error) {
	for _, snap := range snaps {
		if err := timed(snap.Events); err != nil {
			return nil, err
		}
	}
	if len(snaps) == 0 {
		return map[github.Ref]int{}, nil
	}
	var added map[github.Ref]int
	_, err := s.db.Write(ctx, func(tx *sqlite.Tx) error {
		added = map[github.Ref]int{}
		refs := make([]github.Ref, len(snaps))
		for i, snap := range snaps {
			refs[i] = snap.PR.Ref
		}
		prior, err := storedRows(ctx, tx, refs)
		if err != nil {
			return err
		}
		upsert, err := tx.PrepareContext(ctx, upsertPRSQL)
		if err != nil {
			return err
		}
		defer upsert.Close()
		insert, err := tx.PrepareContext(ctx, insertEventSQL)
		if err != nil {
			return err
		}
		defer insert.Close()
		for _, snap := range snaps {
			ref := snap.PR.Ref
			was := prior[ref]
			now, err := s.savePR(ctx, tx, upsert, snap.PR, was, seenAt)
			if err != nil {
				return err
			}
			if snap.Detail != nil {
				d, err := saveDetail(ctx, tx, ref, *snap.Detail, now.detail, seenAt)
				if err != nil {
					return err
				}
				now.detail = &d
			}
			prior[ref] = now
			if err := addEvents(ctx, tx, insert, snap.Events, port.TopicSourceEvents, added); err != nil {
				return err
			}
			for _, m := range snap.Merges {
				if _, err := fillMerged(ctx, tx, m); err != nil {
					return err
				}
			}
		}
		return seeRefs(ctx, tx, refs, seenAt)
	})
	if err != nil {
		return nil, err
	}
	return added, nil
}

// savePR writes pr over was unless no column differs, and records the
// followed or unfollowed event of a scope flip. It returns the row as now
// stored.
func (s *Store) savePR(ctx context.Context, tx *sqlite.Tx, upsert *sql.Stmt, pr dinv.PR, was stored, seenAt time.Time) (stored, error) {
	mergeable, mergeableAt := nextMergeable(was.mergeable, was.mergeableAt, pr.Mergeable, seenAt)
	storedReasons, err := splitList(was.reasons)
	if err != nil {
		return was, err
	}
	reasons := withTracked(append(slices.Clone(pr.Reasons), storedReasons...), was.tracked)
	failing := ""
	if len(pr.FailingChecks) > 0 {
		raw, err := json.Marshal(pr.FailingChecks)
		if err != nil {
			return was, err
		}
		failing = string(raw)
	}
	joined := joinList(reasons)
	res, err := upsert.ExecContext(ctx, pr.Ref.Owner, pr.Ref.Repo, pr.Ref.Number, pr.Title, pr.Author, pr.State, pr.IsDraft, pr.HeadOid, pr.HeadBranch, pr.BaseBranch,
		pr.IsFork, pr.ChecksState, pr.ReviewDecision, joinList(pr.ReviewRequests), pr.URL, ts(pr.CreatedAt), ts(pr.UpdatedAt), ts(pr.MergedAt), ts(pr.ClosedAt), joined,
		ts(pr.BodyEditedAt), pr.ChangedFiles, failing, mergeable, mergeableAt)
	if err != nil {
		return was, err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return was, err
	}
	topics := port.TopicPRs
	if !was.found || joined != was.reasons {
		topics |= port.TopicFollow
	}
	tx.Touch(topics, pr.Ref.Unit())
	ended := pr.MergedAt
	if ended.IsZero() {
		ended = pr.ClosedAt
	}
	now := was
	now.found, now.reasons, now.mergeable, now.mergeableAt = true, joined, mergeable, mergeableAt
	if now.scope, err = scopeOf(reasons, was.ws, pr.State, ts(ended)); err != nil {
		return was, err
	}
	if now.scope.followed == was.scope.followed {
		return now, nil
	}
	at := seenAt.UTC()
	if at.IsZero() {
		at = s.clock().UTC()
	}
	e := flip(pr.Ref, was.scope, now.scope, at, was.followedAt)
	if e.Kind == dinv.KindFollowed && e.At.After(now.followedAt) {
		now.followedAt = e.At
	}
	return now, insertEvents(ctx, tx, []dinv.Event{e})
}

const detailChunk = 500

type storedDetail struct {
	version      int
	readAt, body string
}

// saveDetail writes d over was unless they are equal; a new body notes PRs,
// a new version or read time alone notes nothing. It returns d as stored.
func saveDetail(ctx context.Context, tx *sqlite.Tx, ref github.Ref, d Detail, was *storedDetail, seenAt time.Time) (storedDetail, error) {
	if d.ReadAt.IsZero() {
		d.ReadAt = seenAt
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return storedDetail{}, err
	}
	now := storedDetail{version: d.Version, readAt: ts(d.ReadAt), body: string(raw)}
	switch {
	case was != nil && *was == now:
	case was != nil && was.body == now.body:
		_, err = tx.ExecContext(ctx, `UPDATE pr_details SET version = ?, read_at = ? WHERE owner = ? AND repo = ? AND number = ?`,
			now.version, now.readAt, ref.Owner, ref.Repo, ref.Number)
	default:
		_, err = tx.ExecContext(ctx, `INSERT INTO pr_details (owner, repo, number, version, read_at, body) VALUES (?,?,?,?,?,?)
			ON CONFLICT(owner, repo, number) DO UPDATE SET version = excluded.version, read_at = excluded.read_at, body = excluded.body`,
			ref.Owner, ref.Repo, ref.Number, now.version, now.readAt, now.body)
		tx.Touch(port.TopicPRs, ref.Unit())
	}
	return now, err
}

// Details returns the stored detail of each ref that has one; one query per
// 500 refs, on the read pool.
func (s *Store) Details(ctx context.Context, refs []github.Ref) (map[github.Ref]Detail, error) {
	return s.details(ctx, refs, true)
}

// DetailStamps returns the Version and ReadAt of each ref's stored detail,
// leaving its body unread.
func (s *Store) DetailStamps(ctx context.Context, refs []github.Ref) (map[github.Ref]Detail, error) {
	return s.details(ctx, refs, false)
}

func (s *Store) details(ctx context.Context, refs []github.Ref, withBody bool) (map[github.Ref]Detail, error) {
	out := make(map[github.Ref]Detail, len(refs))
	body := "''"
	if withBody {
		body = "body"
	}
	for chunk := range slices.Chunk(refs, detailChunk) {
		marks, args := refValues(chunk)
		rows, err := s.db.R().QueryContext(ctx, `SELECT owner, repo, number, version, read_at, `+body+` FROM pr_details WHERE (owner, repo, number) IN (VALUES `+marks+`)`, args...)
		if err != nil {
			return nil, err
		}
		err = func() error {
			defer rows.Close()
			for rows.Next() {
				var ref github.Ref
				var d Detail
				var readAt, raw string
				if err := rows.Scan(&ref.Owner, &ref.Repo, &ref.Number, &d.Version, &readAt, &raw); err != nil {
					return err
				}
				if raw != "" {
					if err := json.Unmarshal([]byte(raw), &d); err != nil {
						return fmt.Errorf("detail of %s: %w", ref, err)
					}
				}
				if d.ReadAt, err = parseTS(readAt); err != nil {
					return err
				}
				out[ref] = d
			}
			return rows.Err()
		}()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// storedRows reads what savePR needs of each of refs, absent ones included.
func storedRows(ctx context.Context, tx *sqlite.Tx, refs []github.Ref) (map[github.Ref]stored, error) {
	out := make(map[github.Ref]stored, len(refs))
	for chunk := range slices.Chunk(refs, prReadChunk) {
		marks, args := refValues(chunk)
		rows, err := tx.QueryContext(ctx, `WITH r(owner, repo, number) AS (VALUES `+marks+`)
			SELECT r.owner, r.repo, r.number, p.number IS NOT NULL, coalesce(p.reasons, ''), coalesce(p.mergeable, ''), coalesce(p.mergeable_at, ''),
			 coalesce(p.ws_id, ''), coalesce(p.state, ''), coalesce(iif(p.merged_at != '', p.merged_at, p.closed_at), ''), coalesce(o.tracked, 0),
			 coalesce((SELECT max(e.at) FROM events e WHERE e.provider = 'github' AND e.unit_key = `+unitKeyOf("r")+` AND e.kind = ?), ''),
			 d.number IS NOT NULL, coalesce(d.version, 0), coalesce(d.read_at, ''), coalesce(d.body, '')
			FROM r LEFT JOIN pull_requests p ON p.owner = r.owner AND p.repo = r.repo AND p.number = r.number
			LEFT JOIN overrides o ON o.provider = 'github' AND o.unit_key = `+unitKeyOf("r")+`
			LEFT JOIN pr_details d ON d.owner = r.owner AND d.repo = r.repo AND d.number = r.number`, append(args, dinv.KindFollowed)...)
		if err != nil {
			return nil, err
		}
		err = func() error {
			defer rows.Close()
			for rows.Next() {
				var ref github.Ref
				var st stored
				var ws, state, ended, followed string
				var hasDetail bool
				var d storedDetail
				if err := rows.Scan(&ref.Owner, &ref.Repo, &ref.Number, &st.found, &st.reasons, &st.mergeable, &st.mergeableAt,
					&ws, &state, &ended, &st.tracked, &followed, &hasDetail, &d.version, &d.readAt, &d.body); err != nil {
					return err
				}
				if hasDetail {
					st.detail = &d
				}
				list, err := splitList(st.reasons)
				if err != nil {
					return err
				}
				st.ws = ws
				if st.found {
					if st.scope, err = scopeOf(list, ws, state, ended); err != nil {
						return err
					}
				}
				if st.followedAt, err = parseTS(followed); err != nil {
					return err
				}
				out[ref] = st
			}
			return rows.Err()
		}()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// seeRefs dates the sighting of the stored refs among refs at at, one
// statement per seeChunk refs.
func seeRefs(ctx context.Context, tx *sqlite.Tx, refs []github.Ref, at time.Time) error {
	for chunk := range slices.Chunk(refs, seeChunk) {
		marks, args := refValues(chunk)
		if _, err := tx.ExecContext(ctx, `INSERT INTO pr_seen (owner, repo, number, at)
			SELECT owner, repo, number, ? FROM pull_requests WHERE (owner, repo, number) IN (VALUES `+marks+`) ON CONFLICT DO UPDATE SET at = excluded.at WHERE at != excluded.at`,
			append([]any{ts(at)}, args...)...); err != nil {
			return err
		}
	}
	return nil
}

// SeeRefs records that a search or a read returned refs at at. A sighting
// touches no topic.
func (s *Store) SeeRefs(ctx context.Context, refs []github.Ref, at time.Time) error {
	if len(refs) == 0 {
		return nil
	}
	_, err := s.db.Write(ctx, func(tx *sqlite.Tx) error { return seeRefs(ctx, tx, refs, at) })
	return err
}

// Seen is when a search or a read last returned each open pull request.
func (s *Store) Seen(ctx context.Context) (map[github.Ref]time.Time, error) {
	rows, err := s.db.R().QueryContext(ctx, `SELECT p.owner, p.repo, p.number, COALESCE(s.at, '') FROM pull_requests p LEFT JOIN pr_seen s USING (owner, repo, number) WHERE p.state = ?`, dgh.StateOpen)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[github.Ref]time.Time{}
	for rows.Next() {
		var ref github.Ref
		var raw string
		if err := rows.Scan(&ref.Owner, &ref.Repo, &ref.Number, &raw); err != nil {
			return nil, err
		}
		at, err := parseTS(raw)
		if err != nil {
			return nil, err
		}
		out[ref] = at
	}
	return out, rows.Err()
}

// nextMergeable is the mergeable state to store and when it turned
// CONFLICTING. A value GitHub has not computed (UNKNOWN) or did not send
// keeps the stored one: a conflict does not flip back to mergeable while
// GitHub recomputes it.
func nextMergeable(stored, storedAt, next string, at time.Time) (string, string) {
	switch {
	case next == "" || next == "UNKNOWN":
		return stored, storedAt
	case next != "CONFLICTING":
		return next, ""
	case stored == "CONFLICTING" && storedAt != "":
		return stored, storedAt
	}
	return next, ts(at)
}

type stubRow struct{ requests, decision, mergeable, mergeableAt string }

// TouchStubs refreshes the review requests, the review decision, the
// sighting and the mergeable state of pull requests a search returned unchanged, in one transaction. A
// closed pull request's mergeable state is not followed.
func (s *Store) TouchStubs(ctx context.Context, stubs []github.Stub, seenAt time.Time) error {
	if len(stubs) == 0 {
		return nil
	}
	_, err := s.db.Write(ctx, func(tx *sqlite.Tx) error {
		rows, err := stubRows(ctx, tx, stubs)
		if err != nil {
			return err
		}
		var sighted []github.Ref
		for _, st := range stubs {
			row, ok := rows[st.Ref]
			if !ok {
				continue
			}
			next := row
			if st.State == "" || st.State == "OPEN" {
				next.mergeable, next.mergeableAt = nextMergeable(row.mergeable, row.mergeableAt, st.Mergeable, seenAt)
			}
			next.requests = joinList(st.ReviewRequests)
			next.decision = st.ReviewDecision
			sighted = append(sighted, st.Ref)
			if next == row {
				continue
			}
			if _, err := tx.ExecContext(ctx, `UPDATE pull_requests SET review_requests=?, review_decision=?, mergeable=?, mergeable_at=? WHERE owner=? AND repo=? AND number=?`,
				next.requests, next.decision, next.mergeable, next.mergeableAt, st.Ref.Owner, st.Ref.Repo, st.Ref.Number); err != nil {
				return err
			}
			tx.Touch(port.TopicPRs, st.Ref.Unit())
			rows[st.Ref] = next
		}
		return seeRefs(ctx, tx, sighted, seenAt)
	})
	return err
}

func stubRows(ctx context.Context, tx *sqlite.Tx, stubs []github.Stub) (map[github.Ref]stubRow, error) {
	out := make(map[github.Ref]stubRow, len(stubs))
	for chunk := range slices.Chunk(stubs, prReadChunk) {
		refs := make([]github.Ref, len(chunk))
		for i, st := range chunk {
			refs[i] = st.Ref
		}
		marks, args := refValues(refs)
		rows, err := tx.QueryContext(ctx, `SELECT owner, repo, number, review_requests, review_decision, mergeable, mergeable_at FROM pull_requests
			WHERE (owner, repo, number) IN (VALUES `+marks+`)`, args...)
		if err != nil {
			return nil, err
		}
		err = func() error {
			defer rows.Close()
			for rows.Next() {
				var ref github.Ref
				var r stubRow
				if err := rows.Scan(&ref.Owner, &ref.Repo, &ref.Number, &r.requests, &r.decision, &r.mergeable, &r.mergeableAt); err != nil {
					return err
				}
				out[ref] = r
			}
			return rows.Err()
		}()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) SetReasons(ctx context.Context, ref github.Ref, reasons []string) error {
	_, err := s.db.Write(ctx, func(tx *sqlite.Tx) error {
		tracked, err := isTracked(ctx, tx, ref)
		if err != nil {
			return err
		}
		before, err := scopes(ctx, tx, ref)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE pull_requests SET reasons=? WHERE owner=? AND repo=? AND number=? AND reasons IS NOT ?1`,
			joinList(withTracked(reasons, tracked)), ref.Owner, ref.Repo, ref.Number)
		if err := touchChanged(tx, res, err, port.TopicPRs|port.TopicFollow, ref); err != nil {
			return err
		}
		return s.recordScope(ctx, tx, before, time.Time{}, ref)
	})
	return err
}

// withTracked puts ReasonTracked in reasons exactly when tracked, sorted.
func withTracked(reasons []string, tracked bool) []string {
	out := slices.DeleteFunc(slices.Clone(reasons), func(r string) bool { return r == dinv.ReasonTracked })
	if tracked {
		out = append(out, dinv.ReasonTracked)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func isTracked(ctx context.Context, tx *sqlite.Tx, ref github.Ref) (bool, error) {
	var tracked bool
	err := tx.QueryRowContext(ctx, `SELECT tracked FROM overrides WHERE provider=? AND unit_key=?`, unitArgs(ref)...).Scan(&tracked)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return tracked, err
}

// syncTracked makes a stored pull request's reasons follow its override; one
// not stored yet gets the reason on its first upsert.
func syncTracked(ctx context.Context, tx *sqlite.Tx, ref github.Ref, tracked bool) error {
	var stored string
	err := tx.QueryRowContext(ctx, `SELECT reasons FROM pull_requests WHERE owner=? AND repo=? AND number=?`, ref.Owner, ref.Repo, ref.Number).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	reasons, err := splitList(stored)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE pull_requests SET reasons=? WHERE owner=? AND repo=? AND number=? AND reasons IS NOT ?1`,
		joinList(withTracked(reasons, tracked)), ref.Owner, ref.Repo, ref.Number)
	return touchChanged(tx, res, err, port.TopicPRs|port.TopicFollow, ref)
}

const setWorkspaceSQL = `UPDATE pull_requests SET ws_id=?1, ws_name=?2, ws_status=?3, ws_review=?4, ws_agent=?5, ws_comment=?6, ws_main=?7, ws_manager=?11
 WHERE owner=?8 AND repo=?9 AND number=?10 AND (ws_id, ws_name, ws_status, ws_review, ws_agent, ws_comment, ws_main, ws_manager) IS NOT (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?11)`

func (s *Store) SetWorkspace(ctx context.Context, ref github.Ref, ws dinv.Workspace) error {
	_, err := s.db.Write(ctx, func(tx *sqlite.Tx) error {
		before, err := scopes(ctx, tx, ref)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, setWorkspaceSQL,
			ws.ID, ws.DisplayName, ws.Status, ws.IsReview, joinStates(ws.AgentStates), ws.Comment, ws.IsMain, ref.Owner, ref.Repo, ref.Number, ws.Manager)
		if err := touchChanged(tx, res, err, port.TopicPRs|port.TopicFollow, ref); err != nil {
			return err
		}
		return s.recordScope(ctx, tx, before, time.Time{}, ref)
	})
	return err
}

// ReplaceWorkspaces makes links the set of linked pull requests in one
// transaction, so readers never see the links blank. It writes only the links
// that differ from the stored ones.
func (s *Store) ReplaceWorkspaces(ctx context.Context, links map[github.Ref]dinv.Workspace) error {
	_, err := s.db.Write(ctx, func(tx *sqlite.Tx) error {
		linked, err := workspaceLinks(ctx, tx)
		if err != nil {
			return err
		}
		var drop, set []github.Ref
		for ref := range linked {
			if ws, ok := links[ref]; !ok || linkKey(ws) == (linkColumns{}) {
				drop = append(drop, ref)
			}
		}
		for ref, ws := range links {
			if key := linkKey(ws); key != (linkColumns{}) && key != linked[ref] {
				set = append(set, ref)
			}
		}
		touched := append(drop, set...)
		if len(touched) == 0 {
			return nil
		}
		before, err := scopes(ctx, tx, touched...)
		if err != nil {
			return err
		}
		for _, ref := range drop {
			res, err := tx.ExecContext(ctx, setWorkspaceSQL, "", "", "", false, "", "", false, ref.Owner, ref.Repo, ref.Number, "")
			if err := touchChanged(tx, res, err, port.TopicPRs|port.TopicFollow, ref); err != nil {
				return err
			}
		}
		for _, ref := range set {
			ws := links[ref]
			res, err := tx.ExecContext(ctx, setWorkspaceSQL,
				ws.ID, ws.DisplayName, ws.Status, ws.IsReview, joinStates(ws.AgentStates), ws.Comment, ws.IsMain, ref.Owner, ref.Repo, ref.Number, ws.Manager)
			if err := touchChanged(tx, res, err, port.TopicPRs|port.TopicFollow, ref); err != nil {
				return err
			}
		}
		return s.recordScope(ctx, tx, before, time.Time{}, touched...)
	})
	return err
}

// linkColumns is the workspace columns of a pull request; the zero value is
// no link.
type linkColumns struct {
	id, name, status string
	review           bool
	agent, comment   string
	main             bool
	manager          string
}

func linkKey(ws dinv.Workspace) linkColumns {
	return linkColumns{ws.ID, ws.DisplayName, ws.Status, ws.IsReview, joinStates(ws.AgentStates), ws.Comment, ws.IsMain, ws.Manager}
}

// workspaceLinks reads the workspace columns of every linked pull request.
func workspaceLinks(ctx context.Context, tx *sqlite.Tx) (map[github.Ref]linkColumns, error) {
	rows, err := tx.QueryContext(ctx, `SELECT owner, repo, number, ws_id, ws_name, ws_status, ws_review, ws_agent, ws_comment, ws_main, ws_manager FROM pull_requests
		WHERE ws_id IS NOT '' OR ws_name IS NOT '' OR ws_status IS NOT '' OR ws_review OR ws_agent IS NOT '' OR ws_comment IS NOT '' OR ws_main`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[github.Ref]linkColumns{}
	for rows.Next() {
		var ref github.Ref
		var l linkColumns
		if err := rows.Scan(&ref.Owner, &ref.Repo, &ref.Number, &l.id, &l.name, &l.status, &l.review, &l.agent, &l.comment, &l.main, &l.manager); err != nil {
			return nil, err
		}
		out[ref] = l
	}
	return out, rows.Err()
}

const prColumns = `pr.owner,
 pr.repo,
 pr.number,
 pr.title,
 pr.author,
 pr.state,
 pr.is_draft,
 pr.head_oid,
 pr.head_branch,
 pr.base_branch,
 pr.is_fork,
 pr.checks_state,
 pr.review_decision,
 pr.review_requests,
 pr.url,
 pr.created_at,
 pr.updated_at,
 pr.merged_at,
 pr.closed_at,
 COALESCE((SELECT at FROM pr_seen WHERE owner = pr.owner AND repo = pr.repo AND number = pr.number), ''),
 pr.reasons,
 pr.ws_id,
 pr.ws_name,
 pr.ws_status,
 pr.ws_review,
 pr.ws_agent,
 pr.ws_comment,
 pr.ws_main,
 pr.ws_manager,
 pr.body_edited_at,
 pr.changed_files,
 pr.failing_checks,
 pr.mergeable,
 pr.mergeable_at`

// compareRefs orders refs by owner, repo and number.
func compareRefs(a, b github.Ref) int {
	return cmp.Or(cmp.Compare(a.Owner, b.Owner), cmp.Compare(a.Repo, b.Repo), cmp.Compare(a.Number, b.Number))
}

type scanner interface{ Scan(dest ...any) error }

func scanPR(row scanner) (dinv.PR, error) {
	var pr dinv.PR
	var requests, created, updated, merged, closed, seen, reasons, agent, edited, failing, conflictAt string
	err := row.Scan(&pr.Ref.Owner, &pr.Ref.Repo, &pr.Ref.Number, &pr.Title, &pr.Author, &pr.State, &pr.IsDraft, &pr.HeadOid,
		&pr.HeadBranch, &pr.BaseBranch, &pr.IsFork, &pr.ChecksState, &pr.ReviewDecision, &requests, &pr.URL,
		&created, &updated, &merged, &closed, &seen, &reasons,
		&pr.Workspace.ID, &pr.Workspace.DisplayName, &pr.Workspace.Status, &pr.Workspace.IsReview, &agent, &pr.Workspace.Comment, &pr.Workspace.IsMain,
		&pr.Workspace.Manager, &edited, &pr.ChangedFiles, &failing, &pr.Mergeable, &conflictAt)
	if err != nil {
		return dinv.PR{}, err
	}
	// Unreadable names cost only the names: the next read of the pull
	// request rewrites them.
	if failing != "" && json.Unmarshal([]byte(failing), &pr.FailingChecks) != nil {
		pr.FailingChecks = nil
	}
	for _, l := range []struct {
		dst *[]string
		src string
	}{{&pr.ReviewRequests, requests}, {&pr.Reasons, reasons}} {
		if *l.dst, err = splitList(l.src); err != nil {
			return dinv.PR{}, err
		}
	}
	if pr.Workspace.AgentStates, err = splitStates(agent); err != nil {
		return dinv.PR{}, err
	}
	for _, t := range []struct {
		dst *time.Time
		src string
	}{{&pr.CreatedAt, created}, {&pr.UpdatedAt, updated}, {&pr.MergedAt, merged}, {&pr.ClosedAt, closed}, {&pr.LastSeenAt, seen}, {&pr.BodyEditedAt, edited}, {&pr.MergeableAt, conflictAt}} {
		if *t.dst, err = parseTS(t.src); err != nil {
			return dinv.PR{}, err
		}
	}
	return pr, nil
}

func (s *Store) onePR(ctx context.Context, query string, args ...any) (dinv.PR, bool, error) {
	pr, err := scanPR(s.db.R().QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return dinv.PR{}, false, nil
	}
	if err != nil {
		return dinv.PR{}, false, err
	}
	return pr, true, nil
}

func (s *Store) PR(ctx context.Context, ref github.Ref) (dinv.PR, bool, error) {
	return s.onePR(ctx, `SELECT `+prColumns+` FROM pull_requests pr WHERE pr.owner=? AND pr.repo=? AND pr.number=?`, ref.Owner, ref.Repo, ref.Number)
}

// PRs reads the stored pull requests among refs, leaving out those it lacks.
func (s *Store) PRs(ctx context.Context, refs []github.Ref) (map[github.Ref]dinv.PR, error) {
	out := make(map[github.Ref]dinv.PR, len(refs))
	for chunk := range slices.Chunk(refs, prReadChunk) {
		marks, args := refValues(chunk)
		rows, err := s.db.R().QueryContext(ctx, `SELECT `+prColumns+` FROM pull_requests pr WHERE (pr.owner, pr.repo, pr.number) IN (VALUES `+marks+`)`, args...)
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

// PRsWithOverrides reads every pull request, the most recently updated
// first, with the overrides of those that have one, in one statement.
func (s *Store) PRsWithOverrides(ctx context.Context) ([]dinv.PR, map[github.Ref]dinv.Override, error) {
	rows, err := s.db.R().QueryContext(ctx, `SELECT `+prColumns+`, o.provider IS NOT NULL, COALESCE(o.muted_until, ''),
		COALESCE(o.mute_until_state, 0), COALESCE(o.hold, 0), COALESCE(o.taken_over, 0), COALESCE(o.muted_kind, ''),
		COALESCE(o.muted_waiting_on, ''), COALESCE(o.tracked, 0), COALESCE(o.next_guidance, '')
		FROM pull_requests pr LEFT JOIN overrides o ON o.provider = 'github' AND o.unit_key = `+unitKeyOf("pr")+`
		ORDER BY pr.updated_at DESC, pr.owner, pr.repo, pr.number DESC`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var prs []dinv.PR
	overrides := map[github.Ref]dinv.Override{}
	for rows.Next() {
		var o dinv.Override
		var has bool
		var muted string
		pr, err := scanPR(withTail{rows, []any{&has, &muted, &o.MuteUntilStateChange, &o.Hold, &o.Adopted, &o.MutedKind,
			&o.MutedWaitingOn, &o.Tracked, &o.NextGuidance}})
		if err != nil {
			return nil, nil, err
		}
		prs = append(prs, pr)
		if !has {
			continue
		}
		if o.MutedUntil, err = parseTS(muted); err != nil {
			return nil, nil, err
		}
		o.PR = pr.Ref
		overrides[pr.Ref] = o
	}
	return prs, overrides, rows.Err()
}

// withTail scans a row whose columns go first to a scanner's destinations,
// then to tail.
type withTail struct {
	row  scanner
	tail []any
}

func (w withTail) Scan(dest ...any) error { return w.row.Scan(append(dest, w.tail...)...) }

func listPRsSQL(open bool) string {
	query := `SELECT ` + prColumns + ` FROM pull_requests pr`
	if open {
		query += ` WHERE pr.state='OPEN'`
	}
	return query + ` ORDER BY pr.updated_at DESC, pr.owner, pr.repo, pr.number DESC`
}

func (s *Store) ListPRs(ctx context.Context, open bool) ([]dinv.PR, error) {
	rows, err := s.db.R().QueryContext(ctx, listPRsSQL(open))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var prs []dinv.PR
	for rows.Next() {
		pr, err := scanPR(rows)
		if err != nil {
			return nil, err
		}
		prs = append(prs, pr)
	}
	return prs, rows.Err()
}

// AppendEvents stores the events a source read, each at most once per pull
// request, kind and key, and returns how many were new.
func (s *Store) AppendEvents(ctx context.Context, evs []dinv.Event) (int, error) {
	return s.appendEvents(ctx, evs, port.TopicSourceEvents)
}

const insertEventSQL = `INSERT OR IGNORE INTO events (provider, unit_key, kind, key, actor, at, data) VALUES (?,?,?,?,?,?,?)`

// eventArgs are the insertEventSQL arguments of e.
func eventArgs(e dinv.Event) []any {
	u := sqlite.StoredUnit(e.PR.Unit(), e.Kind, e.Key)
	return []any{string(u.Provider), u.Key, e.Kind, e.Key, e.Actor, ts(e.At), e.Data}
}

// timed refuses events with no time.
func timed(evs []dinv.Event) error {
	for _, e := range evs {
		if e.At.IsZero() {
			return fmt.Errorf("event %s on %s/%s#%d has no time", e.Kind, e.PR.Owner, e.PR.Repo, e.PR.Number)
		}
	}
	return nil
}

func (s *Store) appendEvents(ctx context.Context, evs []dinv.Event, topic port.Topic) (added int, err error) {
	if err := timed(evs); err != nil {
		return 0, err
	}
	_, err = s.db.Write(ctx, func(tx *sqlite.Tx) error {
		added = 0
		stmt, err := tx.PrepareContext(ctx, insertEventSQL)
		if err != nil {
			return err
		}
		defer stmt.Close()
		byRef := map[github.Ref]int{}
		if err := addEvents(ctx, tx, stmt, evs, topic, byRef); err != nil {
			return err
		}
		for _, n := range byRef {
			added += n
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return added, nil
}

// addEvents inserts evs through insert, an insertEventSQL statement, counts
// the new ones per ref in added and touches topic for their refs.
func addEvents(ctx context.Context, tx *sqlite.Tx, insert *sql.Stmt, evs []dinv.Event, topic port.Topic, added map[github.Ref]int) error {
	for _, e := range evs {
		res, err := insert.ExecContext(ctx, eventArgs(e)...)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n > 0 {
			added[e.PR] += int(n)
			tx.Touch(topic, e.PR.Unit())
		}
	}
	return nil
}

// RecordEvent stores an event the loop writes. A keyed one is stored at
// most once per pull request, kind and key; an unkeyed one moves forward a
// nanosecond at a time while the same kind already sits at that time, since
// unkeyed events are unique by pull request, kind and time.
func (s *Store) RecordEvent(ctx context.Context, ev dinv.Event) error {
	if ev.Key != "" {
		_, err := s.appendEvents(ctx, []dinv.Event{ev}, port.TopicLoopEvents)
		return err
	}
	for range 1000 {
		added, err := s.appendEvents(ctx, []dinv.Event{ev}, port.TopicLoopEvents)
		if err != nil || added > 0 {
			return err
		}
		ev.At = ev.At.Add(time.Nanosecond)
	}
	return errors.New("recording " + ev.Kind + ": no free timestamp")
}

func (s *Store) Events(ctx context.Context, ref github.Ref) ([]dinv.Event, error) {
	byPR, err := s.EventsByPR(ctx, []github.Ref{ref})
	if err != nil {
		return nil, err
	}
	return byPR[ref], nil
}

const eventsByUnitsSQL = `SELECT ` + eventColumns + ` FROM events
	WHERE (provider, unit_key) IN (SELECT value->>0, value->>1 FROM json_each(?))
	ORDER BY at, id`

// EventsByPR reads the events of refs, oldest first per pull request, in
// one statement whatever the number of refs.
func (s *Store) EventsByPR(ctx context.Context, refs []github.Ref) (map[github.Ref][]dinv.Event, error) {
	out := make(map[github.Ref][]dinv.Event, len(refs))
	if len(refs) == 0 {
		return out, nil
	}
	keys := make([][2]string, len(refs))
	for i, r := range refs {
		u := r.Unit()
		keys[i] = [2]string{string(u.Provider), u.Key}
	}
	raw, err := json.Marshal(keys)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.R().QueryContext(ctx, eventsByUnitsSQL, string(raw))
	if err != nil {
		return nil, err
	}
	evs, err := scanEvents(rows)
	if err != nil {
		return nil, err
	}
	for _, e := range evs {
		out[e.PR] = append(out[e.PR], e)
	}
	return out, nil
}

// UnitlessEvents reads the events of no pull request, those stored under
// the zero unit or a session unit, oldest first.
func (s *Store) UnitlessEvents(ctx context.Context) ([]dinv.Event, error) {
	rows, err := s.db.R().QueryContext(ctx, `SELECT `+eventColumns+` FROM events WHERE provider IN ('', ?) ORDER BY at, id`, string(unit.Session))
	if err != nil {
		return nil, err
	}
	return scanEvents(rows)
}

func endedSQL(marks string) string {
	return `SELECT DISTINCT unit_key FROM events WHERE provider = 'github' AND unit_key IN (` + marks + `) AND kind=? AND key != ''`
}

const reviewedSQL = `SELECT unit_key, sent_at FROM turns INDEXED BY turns_sent_at
	WHERE sent_at >= ? AND provider = 'github' AND intent = 'review' AND state != ?`

// Reviewed lists the pull requests with a review turn, not withdrawn, sent
// at or after since.
func (s *Store) Reviewed(ctx context.Context, since time.Time) ([]github.Ref, error) {
	// sent_at text drops trailing fraction zeros, so it orders by time only
	// to the second: the SQL bound is a second early and the parsed time
	// decides.
	bound := since.Add(-time.Second).UTC().Format(time.RFC3339)
	rows, err := s.db.R().QueryContext(ctx, reviewedSQL, bound, string(turn.Withdrawn))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []github.Ref
	for rows.Next() {
		var key, sent string
		if err := rows.Scan(&key, &sent); err != nil {
			return nil, err
		}
		at, err := time.Parse(sqlite.TimeLayout, sent)
		if err != nil {
			return nil, err
		}
		if ref := refOf(string(unit.GitHub), key); ref.Number != 0 && !at.Before(since) && !slices.Contains(out, ref) {
			out = append(out, ref)
		}
	}
	return out, rows.Err()
}

// Ended says, per pull request in refs, whether a keyed event of kind exists.
func (s *Store) Ended(ctx context.Context, kind string, refs []github.Ref) (map[github.Ref]bool, error) {
	out := make(map[github.Ref]bool, len(refs))
	for chunk := range slices.Chunk(refs, eventsChunk) {
		marks, args := keyValues(chunk)
		rows, err := s.db.R().QueryContext(ctx, endedSQL(marks), append(args, kind)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var key string
			if err := rows.Scan(&key); err != nil {
				rows.Close()
				return nil, err
			}
			out[refOf(string(unit.GitHub), key)] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

const lastEventSQL = `SELECT ` + eventColumns + ` FROM events WHERE provider = ? AND unit_key = ? AND kind = ?
	ORDER BY at DESC, id DESC LIMIT 1`

// LastEvent is the newest event of kind on ref.
func (s *Store) LastEvent(ctx context.Context, ref github.Ref, kind string) (dinv.Event, bool, error) {
	rows, err := s.db.R().QueryContext(ctx, lastEventSQL, append(unitArgs(ref), kind)...)
	if err != nil {
		return dinv.Event{}, false, err
	}
	evs, err := scanEvents(rows)
	if err != nil || len(evs) == 0 {
		return dinv.Event{}, false, err
	}
	return evs[0], true, nil
}

// FirstRoundBlocks maps each unit whose round is blocked to its block, ended
// as dinv.ActiveBlock ends it, the rule a pull request's row shows.
func (s *Store) FirstRoundBlocks(ctx context.Context) (map[unit.Unit]port.Event, error) {
	rows, err := s.db.R().QueryContext(ctx, `SELECT `+eventColumns+` FROM events WHERE kind IN (?, ?) ORDER BY at, id`, dinv.KindRoundBlocked, dinv.KindRoundUnblocked)
	if err != nil {
		return nil, err
	}
	evs, err := scanUnitEvents(rows)
	if err != nil {
		return nil, err
	}
	byUnit := map[unit.Unit][]dinv.Event{}
	for _, e := range evs {
		byUnit[e.unit] = append(byUnit[e.unit], e.Event)
	}
	var since time.Time
	for u, evs := range byUnit {
		b, ok := dinv.ActiveBlock(evs, "")
		if !ok {
			delete(byUnit, u)
			continue
		}
		if since.IsZero() || b.At.Before(since) {
			since = b.At
		}
	}
	if len(byUnit) == 0 {
		return map[unit.Unit]port.Event{}, nil
	}
	viewer, err := s.Viewer(ctx, unit.GitHub)
	if err != nil {
		return nil, err
	}
	rows, err = s.db.R().QueryContext(ctx, `SELECT `+eventColumns+` FROM events WHERE kind IN (?, ?, ?) AND at >= ? ORDER BY at, id`,
		dinv.KindRoundSent, dinv.KindRoundHeld, dinv.KindReview, ts(since))
	if err != nil {
		return nil, err
	}
	later, err := scanUnitEvents(rows)
	if err != nil {
		return nil, err
	}
	for _, e := range later {
		if evs, ok := byUnit[e.unit]; ok {
			byUnit[e.unit] = append(evs, e.Event)
		}
	}
	out := map[unit.Unit]port.Event{}
	for u, evs := range byUnit {
		slices.SortStableFunc(evs, func(a, b dinv.Event) int { return a.At.Compare(b.At) })
		if b, ok := dinv.ActiveBlock(evs, viewer); ok {
			out[u] = port.Event{Unit: u, Kind: b.Kind, Key: b.Key, Data: b.Data, At: b.At}
		}
	}
	return out, nil
}

// RecordUnitEvent stores an event the loop writes on any unit, as
// RecordEvent stores one on a pull request.
func (s *Store) RecordUnitEvent(ctx context.Context, ev port.Event) error {
	return sqlite.New(s.db).RecordEvents(ctx, ev)
}

// unitEvent is a stored event with the unit it is stored under.
type unitEvent struct {
	dinv.Event
	unit unit.Unit
}

func scanUnitEvents(rows *sql.Rows) ([]unitEvent, error) {
	defer rows.Close()
	var out []unitEvent
	for rows.Next() {
		var e unitEvent
		var provider, at string
		if err := rows.Scan(&provider, &e.unit.Key, &e.Kind, &e.Key, &e.Actor, &at, &e.Data); err != nil {
			return nil, err
		}
		e.unit.Provider = unit.ProviderID(provider)
		e.PR = refOf(provider, e.unit.Key)
		var err error
		if e.At, err = parseTS(at); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

const eventColumns = `provider, unit_key, kind, key, actor, at, data`

// scanEvents reads every row and closes rows.
func scanEvents(rows *sql.Rows) ([]dinv.Event, error) {
	defer rows.Close()
	var out []dinv.Event
	for rows.Next() {
		var e dinv.Event
		var provider, key, at string
		if err := rows.Scan(&provider, &key, &e.Kind, &e.Key, &e.Actor, &at, &e.Data); err != nil {
			return nil, err
		}
		e.PR = refOf(provider, key)
		var err error
		if e.At, err = parseTS(at); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) SetOverride(ctx context.Context, o dinv.Override) error {
	_, err := s.writeOverride(ctx, o, nil)
	return err
}

// SetOverrideIf writes o only while the stored override is still was, the
// row o was derived from; written is false when another writer came between.
func (s *Store) SetOverrideIf(ctx context.Context, was, o dinv.Override) (written bool, err error) {
	return s.writeOverride(ctx, o, &was)
}

func (s *Store) writeOverride(ctx context.Context, o dinv.Override, was *dinv.Override) (written bool, err error) {
	query := `INSERT INTO overrides (` + overrideWriteColumns + `) VALUES (?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(provider, unit_key) DO UPDATE SET muted_until=excluded.muted_until, mute_until_state=excluded.mute_until_state,
		 hold=excluded.hold, taken_over=excluded.taken_over, muted_kind=excluded.muted_kind, muted_waiting_on=excluded.muted_waiting_on,
		 tracked=excluded.tracked
		 WHERE (muted_until, mute_until_state, hold, taken_over, muted_kind, muted_waiting_on, tracked) IS NOT
		 (excluded.muted_until, excluded.mute_until_state, excluded.hold, excluded.taken_over, excluded.muted_kind, excluded.muted_waiting_on, excluded.tracked)`
	args := append(unitArgs(o.PR), ts(o.MutedUntil), o.MuteUntilStateChange, o.Hold, o.Adopted, o.MutedKind, o.MutedWaitingOn, o.Tracked)
	if was != nil {
		guard := []any{ts(was.MutedUntil), was.MuteUntilStateChange, was.Hold, was.Adopted, was.MutedKind, was.MutedWaitingOn, was.Tracked}
		if (dinv.Override{PR: was.PR, NextGuidance: was.NextGuidance}) == *was {
			query += ` AND (muted_until, mute_until_state, hold, taken_over, muted_kind, muted_waiting_on, tracked) IS (?,?,?,?,?,?,?)`
			args = append(args, guard...)
		} else {
			query = `UPDATE overrides SET muted_until=?, mute_until_state=?, hold=?, taken_over=?, muted_kind=?, muted_waiting_on=?, tracked=?
			 WHERE provider=? AND unit_key=? AND (muted_until, mute_until_state, hold, taken_over, muted_kind, muted_waiting_on, tracked) IS (?,?,?,?,?,?,?)
			 AND (muted_until, mute_until_state, hold, taken_over, muted_kind, muted_waiting_on, tracked) IS NOT (?,?,?,?,?,?,?)`
			cols := args[2:]
			args = append(append(append(append([]any{}, cols...), unitArgs(o.PR)...), guard...), cols...)
		}
	}
	_, err = s.db.Write(ctx, func(tx *sqlite.Tx) error {
		res, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		written = n > 0
		if written {
			tx.Touch(port.TopicOverrides, o.PR.Unit())
		} else if was != nil {
			return nil
		}
		before, err := scopes(ctx, tx, o.PR)
		if err != nil {
			return err
		}
		if err := syncTracked(ctx, tx, o.PR, o.Tracked); err != nil {
			return err
		}
		return s.recordScope(ctx, tx, before, time.Time{}, o.PR)
	})
	return written, err
}

// ClearMute clears ref's "mute until state change" when it still mutes kind
// waiting on waitingOn, writing the mute columns alone; cleared says it did.
func (s *Store) ClearMute(ctx context.Context, ref github.Ref, kind, waitingOn string) (cleared bool, err error) {
	_, err = s.db.Write(ctx, func(tx *sqlite.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE overrides SET muted_until='', mute_until_state=0, muted_kind='', muted_waiting_on=''
		 WHERE provider=? AND unit_key=? AND mute_until_state=1 AND muted_kind=? AND muted_waiting_on=?`,
			append(unitArgs(ref), kind, waitingOn)...)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if cleared = n > 0; cleared {
			tx.Touch(port.TopicOverrides, ref.Unit())
		}
		return nil
	})
	return cleared, err
}

// SetAdopted marks ref taken over, writing that column alone.
func (s *Store) SetAdopted(ctx context.Context, ref github.Ref) error {
	return s.exec(ctx, port.TopicOverrides, ref, `INSERT INTO overrides (provider, unit_key, taken_over) VALUES (?,?,1)
	 ON CONFLICT(provider, unit_key) DO UPDATE SET taken_over=1 WHERE taken_over IS NOT 1`,
		unitArgs(ref)...)
}

// Tracked lists the pull requests the user tracks.
func (s *Store) Tracked(ctx context.Context) ([]github.Ref, error) {
	rows, err := s.db.R().QueryContext(ctx, `SELECT unit_key FROM overrides WHERE provider = 'github' AND tracked=1`)
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

const (
	overrideWriteColumns = `provider, unit_key, muted_until, mute_until_state, hold, taken_over, muted_kind, muted_waiting_on, tracked`
	overrideColumns      = overrideWriteColumns + `, next_guidance`
)

func scanOverride(row scanner) (dinv.Override, error) {
	var o dinv.Override
	var provider, key, muted string
	if err := row.Scan(&provider, &key, &muted, &o.MuteUntilStateChange, &o.Hold, &o.Adopted, &o.MutedKind, &o.MutedWaitingOn,
		&o.Tracked, &o.NextGuidance); err != nil {
		return dinv.Override{}, err
	}
	o.PR = refOf(provider, key)
	var err error
	if o.MutedUntil, err = parseTS(muted); err != nil {
		return dinv.Override{}, err
	}
	return o, nil
}

// SetNextGuidance keeps guidance that came after its round, for the next one.
func (s *Store) SetNextGuidance(ctx context.Context, ref github.Ref, guidance string) error {
	return s.exec(ctx, port.TopicOverrides, ref, `INSERT INTO overrides (provider, unit_key, next_guidance) VALUES (?,?,?)
	 ON CONFLICT(provider, unit_key) DO UPDATE SET next_guidance=excluded.next_guidance WHERE next_guidance IS NOT excluded.next_guidance`,
		append(unitArgs(ref), guidance)...)
}

// KeepNextGuidance keeps the guidance of a round that was not sent, or whose
// prompt was not seen landing, for the next round. Guidance given since wins.
func (s *Store) KeepNextGuidance(ctx context.Context, ref github.Ref, guidance string) error {
	return s.exec(ctx, port.TopicOverrides, ref, `INSERT INTO overrides (provider, unit_key, next_guidance) VALUES (?,?,?)
	 ON CONFLICT(provider, unit_key) DO UPDATE SET next_guidance=excluded.next_guidance WHERE next_guidance='' AND excluded.next_guidance IS NOT ''`,
		append(unitArgs(ref), guidance)...)
}

// TakeNextGuidance returns the kept guidance and clears it.
func (s *Store) TakeNextGuidance(ctx context.Context, ref github.Ref) (string, error) {
	var guidance string
	_, err := s.db.Write(ctx, func(tx *sqlite.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT next_guidance FROM overrides WHERE provider=? AND unit_key=?`,
			unitArgs(ref)...).Scan(&guidance)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && guidance == "") {
			return nil
		}
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE overrides SET next_guidance='' WHERE provider=? AND unit_key=?`,
			unitArgs(ref)...)
		return touchChanged(tx, res, err, port.TopicOverrides, ref)
	})
	if err != nil {
		return "", err
	}
	return guidance, nil
}

func (s *Store) Override(ctx context.Context, ref github.Ref) (dinv.Override, error) {
	o, err := scanOverride(s.db.R().QueryRowContext(ctx, `SELECT `+overrideColumns+` FROM overrides WHERE provider=? AND unit_key=?`, unitArgs(ref)...))
	if errors.Is(err, sql.ErrNoRows) {
		return dinv.Override{PR: ref}, nil
	}
	return o, err
}

func (s *Store) Overrides(ctx context.Context) (map[github.Ref]dinv.Override, error) {
	rows, err := s.db.R().QueryContext(ctx, `SELECT `+overrideColumns+` FROM overrides WHERE provider = 'github'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[github.Ref]dinv.Override{}
	for rows.Next() {
		o, err := scanOverride(rows)
		if err != nil {
			return nil, err
		}
		out[o.PR] = o
	}
	return out, rows.Err()
}

func (s *Store) Meta(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.R().QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// MetaPrefixed is every meta value whose key starts with prefix, by key.
func (s *Store) MetaPrefixed(ctx context.Context, prefix string) (map[string]string, error) {
	rows, err := s.db.R().QueryContext(ctx, `SELECT key, value FROM meta WHERE substr(key, 1, length(?)) = ?`, prefix, prefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

func (s *Store) SetMeta(ctx context.Context, key, value string) error {
	_, err := s.db.Write(ctx, func(tx *sqlite.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES (?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value WHERE value IS NOT excluded.value`, key, value)
		return touchChanged(tx, res, err, port.TopicMeta, github.Ref{})
	})
	return err
}

// SetMetaNoting is SetMeta that also notes the extra topics in the same
// transaction.
func (s *Store) SetMetaNoting(ctx context.Context, key, value string, extra port.Topic) error {
	_, err := s.db.Write(ctx, func(tx *sqlite.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO meta (key, value) VALUES (?, ?)`, key, value); err != nil {
			return err
		}
		tx.Touch(port.TopicMeta | extra)
		return nil
	})
	return err
}

// SetMetas writes every key of kv in one transaction and notes Meta once
// when any value changed.
func (s *Store) SetMetas(ctx context.Context, kv map[string]string) error {
	if len(kv) == 0 {
		return nil
	}
	_, err := s.db.Write(ctx, func(tx *sqlite.Tx) error {
		changed := false
		for k, v := range kv {
			res, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES (?, ?)
			 ON CONFLICT(key) DO UPDATE SET value=excluded.value WHERE value IS NOT excluded.value`, k, v)
			if err != nil {
				return err
			}
			if n, err := res.RowsAffected(); err != nil {
				return err
			} else if n > 0 {
				changed = true
			}
		}
		if changed {
			tx.Touch(port.TopicMeta)
		}
		return nil
	})
	return err
}

const viewerKeyPrefix = "viewer:"

// SetViewer stores the provider's viewer login, drops the unprefixed row an
// earlier schema kept, and notes TopicViewer when the login changed.
func (s *Store) SetViewer(ctx context.Context, provider unit.ProviderID, login string) error {
	key := viewerKeyPrefix + string(provider)
	_, err := s.db.Write(ctx, func(tx *sqlite.Tx) error {
		var old string
		err := tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, key).Scan(&old)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM meta WHERE key='viewer'`); err != nil {
			return err
		}
		if err == nil && old == login {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO meta (key, value) VALUES (?, ?)`, key, login); err != nil {
			return err
		}
		tx.Touch(port.TopicViewer)
		return nil
	})
	return err
}

// Viewer is the provider's stored viewer login, "" before it is known.
func (s *Store) Viewer(ctx context.Context, provider unit.ProviderID) (string, error) {
	return s.Meta(ctx, viewerKeyPrefix+string(provider))
}

// Viewers is every stored viewer login by provider.
func (s *Store) Viewers(ctx context.Context) (map[unit.ProviderID]string, error) {
	raw, err := s.MetaPrefixed(ctx, viewerKeyPrefix)
	if err != nil {
		return nil, err
	}
	out := map[unit.ProviderID]string{}
	for k, v := range raw {
		out[unit.ProviderID(strings.TrimPrefix(k, viewerKeyPrefix))] = v
	}
	return out, nil
}

// SaveTurn keeps the text a turn was sent with, its guide sources and the
// hash of the bundled guide of its kind at the time. The text is stored as
// deflated sections shared between turns.
func (s *Store) SaveTurn(ctx context.Context, t dinv.StoredTurn, now time.Time) error {
	parts := splitParts(t.Text)
	shas := make([]string, len(parts))
	for i, p := range parts {
		shas[i] = partSHA(p)
	}
	list, err := json.Marshal(shas)
	if err != nil {
		return err
	}
	_, err = s.db.Write(ctx, func(tx *sqlite.Tx) error {
		have, err := storedParts(ctx, tx, string(list))
		if err != nil {
			return err
		}
		for i, p := range parts {
			if have[shas[i]] {
				continue
			}
			body, err := deflatePart(p)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO instruction_parts (sha, body) VALUES (?, ?)`, shas[i], body); err != nil {
				return err
			}
			have[shas[i]] = true
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO turn_instructions (sha, kind, repo, parts, created_at, bundled_sha, sources) VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(sha) DO UPDATE SET parts=excluded.parts, created_at=excluded.created_at, bundled_sha=excluded.bundled_sha, sources=excluded.sources`,
			t.ID, t.Kind, t.Repo, string(list), now.UTC().Format(timeLayout), t.BundledSHA, strings.Join(t.Sources, "\n")); err != nil {
			return err
		}
		tx.Touch(port.TopicRounds)
		return nil
	})
	return err
}

func storedParts(ctx context.Context, tx *sqlite.Tx, list string) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT sha FROM instruction_parts WHERE sha IN (SELECT value FROM json_each(?))`, list)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	have := map[string]bool{}
	for rows.Next() {
		var sha string
		if err := rows.Scan(&sha); err != nil {
			return nil, err
		}
		have[sha] = true
	}
	return have, rows.Err()
}

// Turn is the turn kept under id; ok is false once it is gone or never was.
func (s *Store) Turn(ctx context.Context, id string) (dinv.StoredTurn, bool, error) {
	t := dinv.StoredTurn{ID: id}
	found := false
	err := s.db.Read(ctx, func(tx *sql.Tx) error {
		var parts, sources string
		err := tx.QueryRowContext(ctx, `SELECT kind, repo, parts, bundled_sha, sources FROM turn_instructions WHERE sha = ?`, id).
			Scan(&t.Kind, &t.Repo, &parts, &t.BundledSHA, &sources)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if t.Text, err = readParts(ctx, tx, parts); err != nil {
			return err
		}
		if sources != "" {
			t.Sources = strings.Split(sources, "\n")
		}
		found = true
		return nil
	})
	if err != nil || !found {
		return dinv.StoredTurn{}, false, err
	}
	return t, true, nil
}

const latestBundleSQL = `SELECT bundled_sha FROM turn_instructions WHERE kind = ? AND repo = ? ORDER BY created_at DESC LIMIT 1`

// LastBundledSHA is the bundled guide hash of the latest round of kind in
// repo; ok is false before any.
func (s *Store) LastBundledSHA(ctx context.Context, kind, repo string) (string, bool, error) {
	var sha string
	err := s.db.R().QueryRowContext(ctx, latestBundleSQL, kind, repo).Scan(&sha)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return sha, err == nil, err
}
