// Package inventorytest keeps the pull requests a host test holds in tables
// of its own in the test's store, the fixture the GitHub plugin's reports
// are derived from in the host's tests; the host's schema has no such
// tables. Only tests import it.
package inventorytest

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	dgh "github.com/wloophq/wloop/internal/domain/github"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/event"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

const timeLayout = event.TimeLayout

const eventsChunk = 100

// Store is a host test's inventory with the pull requests it holds.
type Store struct {
	*inventory.Store
	db    *sqlite.DB
	clock func() time.Time
}

// Of is st holding its test's pull requests.
func Of(st *inventory.Store) *Store {
	if s, ok := held.Load(st); ok {
		return s.(*Store)
	}
	s, loaded := held.LoadOrStore(st, &Store{Store: st, db: st.DB(), clock: st.Now})
	if !loaded {
		if _, err := st.DB().Write(context.Background(), func(tx *sqlite.Tx) error {
			_, err := tx.ExecContext(context.Background(), fixtureTables)
			return err
		}); err != nil {
			panic(fmt.Sprintf("inventorytest: creating the fixture's tables: %v", err))
		}
		st.UsePRs(s.(*Store))
	}
	return s.(*Store)
}

// fixtureTables are the fixture's pull requests, their sightings and full
// reads, in the shape the GitHub plugin keeps them.
const fixtureTables = `
CREATE TABLE IF NOT EXISTS pull_requests (
  owner TEXT NOT NULL, repo TEXT NOT NULL, number INTEGER NOT NULL,
  title TEXT NOT NULL DEFAULT '', author TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL DEFAULT 'OPEN', is_draft INTEGER NOT NULL DEFAULT 0,
  head_oid TEXT NOT NULL DEFAULT '', head_branch TEXT NOT NULL DEFAULT '', base_branch TEXT NOT NULL DEFAULT '',
  is_fork INTEGER NOT NULL DEFAULT 0, checks_state TEXT NOT NULL DEFAULT '', review_decision TEXT NOT NULL DEFAULT '',
  review_requests TEXT NOT NULL DEFAULT '',
  url TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT '', updated_at TEXT NOT NULL DEFAULT '', merged_at TEXT NOT NULL DEFAULT '', closed_at TEXT NOT NULL DEFAULT '',
  body_edited_at TEXT NOT NULL DEFAULT '', changed_files INTEGER NOT NULL DEFAULT 0,
  reasons TEXT NOT NULL DEFAULT '',
  ws_id TEXT NOT NULL DEFAULT '', ws_name TEXT NOT NULL DEFAULT '', ws_status TEXT NOT NULL DEFAULT '', ws_review INTEGER NOT NULL DEFAULT 0, ws_agent TEXT NOT NULL DEFAULT '',
  ws_comment TEXT NOT NULL DEFAULT '', ws_main INTEGER NOT NULL DEFAULT 0, ws_manager TEXT NOT NULL DEFAULT '',
  failing_checks TEXT NOT NULL DEFAULT '',
  mergeable TEXT NOT NULL DEFAULT '',
  mergeable_at TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (owner, repo, number)
);
CREATE INDEX IF NOT EXISTS pr_state ON pull_requests(state, updated_at);
CREATE TABLE IF NOT EXISTS pr_seen (
  owner TEXT NOT NULL, repo TEXT NOT NULL, number INTEGER NOT NULL,
  at TEXT NOT NULL,
  PRIMARY KEY (owner, repo, number)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS pr_details (
  owner TEXT NOT NULL, repo TEXT NOT NULL, number INTEGER NOT NULL,
  version INTEGER NOT NULL,
  read_at TEXT NOT NULL,
  body TEXT NOT NULL,
  PRIMARY KEY (owner, repo, number)
) WITHOUT ROWID;`

// held are the stores Of gave, by the inventory they hold.
var held sync.Map

// List are the pull requests the test holds, the open ones alone when open.
func (s *Store) List(ctx context.Context, open bool) ([]dinv.PR, error) { return s.ListPRs(ctx, open) }

const prReadChunk = 400

const seeChunk = 500

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

// Snapshot is one pull request as GitHub returned it, ready to store.
type Snapshot struct {
	PR     dinv.PR           // Reasons are merged with the stored ones; LastSeenAt is ignored
	Events []dinv.Event      // the timeline; those already stored are ignored
	Merges []dinv.Event      // merged events that fill a stored one lacking the merger or the commit
	Detail *inventory.Detail // nil keeps the stored one
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

// SaveSnapshots stores snaps. The events, host data, go to the host
// through the broker first: the timeline events the host does not hold
// yet, the merges that fill a stored one, and a followed or unfollowed
// event for each pull request whose scope flips, derived from a read of
// the stored rows. Then one write transaction rewrites a row only when a
// column differs, records every ref as seen, and writes a detail only when
// it differs from the stored one, noting it only when its body does. A
// write that fails after the broker took the events leaves the rows as they
// were, so the next save derives the same events again and the host keeps
// each once. seenAt dates the sighting, a new conflict, the flips and a
// detail without ReadAt. It returns the events added per ref that gained
// any.
func (s *Store) SaveSnapshots(ctx context.Context, snaps []Snapshot, seenAt time.Time) (map[github.Ref]int, error) {
	for _, snap := range snaps {
		if err := timed(snap.Events); err != nil {
			return nil, err
		}
	}
	if len(snaps) == 0 {
		return map[github.Ref]int{}, nil
	}
	refs := make([]github.Ref, len(snaps))
	kinds := map[string]bool{dinv.KindFollowed: true, dinv.KindUnfollowed: true}
	for i, snap := range snaps {
		refs[i] = snap.PR.Ref
		for _, e := range snap.Events {
			kinds[e.Kind] = true
		}
	}
	held, err := s.hostEvents(ctx, refs, slices.Sorted(maps.Keys(kinds))...)
	if err != nil {
		return nil, err
	}
	known := map[eventID]bool{}
	flips := flipLog{}
	for _, e := range held {
		known[idOf(e)] = true
		flips.saw(e)
	}
	prior, err := storedRows(ctx, s.db.R(), refs)
	if err != nil {
		return nil, err
	}
	added := map[github.Ref]int{}
	var evs []dinv.Event
	for _, snap := range snaps {
		ref := snap.PR.Ref
		was := prior[ref]
		was.followedAt = flips.followedAt[ref]
		w, err := s.advance(snap.PR, was, seenAt)
		if err != nil {
			return nil, err
		}
		prior[ref] = w.now
		for _, e := range snap.Events {
			if id := idOf(e); !known[id] {
				known[id] = true
				added[e.PR]++
				evs = append(evs, e)
			}
		}
		evs = append(evs, snap.Merges...)
		if w.flip != nil && flips.wants(*w.flip) {
			evs = append(evs, *w.flip)
			flips.saw(*w.flip)
		}
	}
	if err := s.recordEvents(ctx, evs); err != nil {
		return nil, err
	}
	_, err = s.db.Write(ctx, func(tx *sqlite.Tx) error {
		prior, err := storedRows(ctx, tx, refs)
		if err != nil {
			return err
		}
		upsert, err := tx.PrepareContext(ctx, upsertPRSQL)
		if err != nil {
			return err
		}
		defer upsert.Close()
		for _, snap := range snaps {
			ref := snap.PR.Ref
			now, err := s.savePR(ctx, upsert, tx, snap.PR, prior[ref], seenAt)
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
		}
		return seeRefs(ctx, tx, refs, seenAt)
	})
	if err != nil {
		return nil, err
	}
	return added, nil
}

// eventID is what makes an event one: its pull request, kind and key, or,
// unkeyed, its time.
type eventID struct {
	ref       github.Ref
	kind, key string
}

func idOf(e dinv.Event) eventID {
	key := e.Key
	if key == "" {
		key = "@" + ts(e.At)
	}
	return eventID{e.PR, e.Kind, key}
}

// rowWrite is what saving a snapshot over a stored row writes: the row as
// it then stands, its columns the store computes, and the followed or
// unfollowed event of a scope flip.
type rowWrite struct {
	now                    stored
	reasons, failing       string
	mergeable, mergeableAt string
	flip                   *dinv.Event
}

// advance is what saving pr over was writes, seen at seenAt.
func (s *Store) advance(pr dinv.PR, was stored, seenAt time.Time) (rowWrite, error) {
	w := rowWrite{now: was}
	w.mergeable, w.mergeableAt = nextMergeable(was.mergeable, was.mergeableAt, pr.Mergeable, seenAt)
	storedReasons, err := splitList(was.reasons)
	if err != nil {
		return w, err
	}
	reasons := withTracked(append(slices.Clone(pr.Reasons), storedReasons...), was.tracked)
	if len(pr.FailingChecks) > 0 {
		raw, err := json.Marshal(pr.FailingChecks)
		if err != nil {
			return w, err
		}
		w.failing = string(raw)
	}
	w.reasons = joinList(reasons)
	ended := pr.MergedAt
	if ended.IsZero() {
		ended = pr.ClosedAt
	}
	w.now.found, w.now.reasons, w.now.mergeable, w.now.mergeableAt = true, w.reasons, w.mergeable, w.mergeableAt
	if w.now.scope, err = scopeOf(reasons, was.ws, pr.State, ts(ended)); err != nil {
		return w, err
	}
	if w.now.scope.followed == was.scope.followed {
		return w, nil
	}
	at := seenAt.UTC()
	if at.IsZero() {
		at = s.clock().UTC()
	}
	e := flip(pr.Ref, was.scope, w.now.scope, at, was.followedAt)
	if e.Kind == dinv.KindFollowed && e.At.After(w.now.followedAt) {
		w.now.followedAt = e.At
	}
	w.flip = &e
	return w, nil
}

// savePR writes pr over was unless no column differs, and returns the row
// as now stored.
func (s *Store) savePR(ctx context.Context, upsert *sql.Stmt, tx *sqlite.Tx, pr dinv.PR, was stored, seenAt time.Time) (stored, error) {
	w, err := s.advance(pr, was, seenAt)
	if err != nil {
		return was, err
	}
	res, err := upsert.ExecContext(ctx, pr.Ref.Owner, pr.Ref.Repo, pr.Ref.Number, pr.Title, pr.Author, pr.State, pr.IsDraft, pr.HeadOid, pr.HeadBranch, pr.BaseBranch,
		pr.IsFork, pr.ChecksState, pr.ReviewDecision, joinList(pr.ReviewRequests), pr.URL, ts(pr.CreatedAt), ts(pr.UpdatedAt), ts(pr.MergedAt), ts(pr.ClosedAt), w.reasons,
		ts(pr.BodyEditedAt), pr.ChangedFiles, w.failing, w.mergeable, w.mergeableAt)
	if err != nil {
		return was, err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return was, err
	}
	topics := port.TopicPRs
	if !was.found || w.reasons != was.reasons {
		topics |= port.TopicFollow
	}
	tx.Touch(topics, pr.Ref.Unit())
	return w.now, nil
}

const detailChunk = 500

type storedDetail struct {
	version      int
	readAt, body string
}

// saveDetail writes d over was unless they are equal; a new body notes PRs,
// a new version or read time alone notes nothing. It returns d as stored.
func saveDetail(ctx context.Context, tx *sqlite.Tx, ref github.Ref, d inventory.Detail, was *storedDetail, seenAt time.Time) (storedDetail, error) {
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
func (s *Store) Details(ctx context.Context, refs []github.Ref) (map[github.Ref]inventory.Detail, error) {
	return s.details(ctx, refs, true)
}

func (s *Store) details(ctx context.Context, refs []github.Ref, withBody bool) (map[github.Ref]inventory.Detail, error) {
	out := make(map[github.Ref]inventory.Detail, len(refs))
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
				var d inventory.Detail
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
func storedRows(ctx context.Context, tx sqlite.Reader, refs []github.Ref) (map[github.Ref]stored, error) {
	out := make(map[github.Ref]stored, len(refs))
	for chunk := range slices.Chunk(refs, prReadChunk) {
		marks, args := refValues(chunk)
		rows, err := tx.QueryContext(ctx, `WITH r(owner, repo, number) AS (VALUES `+marks+`)
			SELECT r.owner, r.repo, r.number, p.number IS NOT NULL, coalesce(p.reasons, ''), coalesce(p.mergeable, ''), coalesce(p.mergeable_at, ''),
			 coalesce(p.ws_id, ''), coalesce(p.state, ''), coalesce(iif(p.merged_at != '', p.merged_at, p.closed_at), ''), coalesce(o.tracked, 0),
			 d.number IS NOT NULL, coalesce(d.version, 0), coalesce(d.read_at, ''), coalesce(d.body, '')
			FROM r LEFT JOIN pull_requests p ON p.owner = r.owner AND p.repo = r.repo AND p.number = r.number
			LEFT JOIN overrides o ON o.provider = 'github' AND o.unit_key = `+unitKeyOf("r")+`
			LEFT JOIN pr_details d ON d.owner = r.owner AND d.repo = r.repo AND d.number = r.number`, args...)
		if err != nil {
			return nil, err
		}
		err = func() error {
			defer rows.Close()
			for rows.Next() {
				var ref github.Ref
				var st stored
				var ws, state, ended string
				var hasDetail bool
				var d storedDetail
				if err := rows.Scan(&ref.Owner, &ref.Repo, &ref.Number, &st.found, &st.reasons, &st.mergeable, &st.mergeableAt,
					&ws, &state, &ended, &st.tracked, &hasDetail, &d.version, &d.readAt, &d.body); err != nil {
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

// SetReasons replaces ref's follow reasons, recording on the host first
// the followed or unfollowed event of a scope flip.
func (s *Store) SetReasons(ctx context.Context, ref github.Ref, reasons []string) error {
	tracked, err := isTracked(ctx, s.db.R(), ref)
	if err != nil {
		return err
	}
	if err := s.recordFlipsOf(ctx, []github.Ref{ref}, time.Time{}, func(_ github.Ref, was scope) (scope, bool, error) {
		return was.with(withTracked(reasons, tracked), was.ws), true, nil
	}); err != nil {
		return err
	}
	_, err = s.db.Write(ctx, func(tx *sqlite.Tx) error {
		tracked, err := isTracked(ctx, tx, ref)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE pull_requests SET reasons=? WHERE owner=? AND repo=? AND number=? AND reasons IS NOT ?1`,
			joinList(withTracked(reasons, tracked)), ref.Owner, ref.Repo, ref.Number)
		if err := touchChanged(tx, res, err, port.TopicPRs|port.TopicFollow, ref); err != nil {
			return err
		}
		return nil
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

func isTracked(ctx context.Context, tx sqlite.Reader, ref github.Ref) (bool, error) {
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

// SetWorkspace links ref to ws, recording on the host first the followed
// or unfollowed event of a scope flip.
func (s *Store) SetWorkspace(ctx context.Context, ref github.Ref, ws dinv.Workspace) error {
	if err := s.recordFlipsOf(ctx, []github.Ref{ref}, time.Time{}, func(_ github.Ref, was scope) (scope, bool, error) {
		return was.with(was.reasons, ws.ID), true, nil
	}); err != nil {
		return err
	}
	_, err := s.db.Write(ctx, func(tx *sqlite.Tx) error {
		res, err := tx.ExecContext(ctx, setWorkspaceSQL,
			ws.ID, ws.DisplayName, ws.Status, ws.IsReview, joinStates(ws.AgentStates), ws.Comment, ws.IsMain, ref.Owner, ref.Repo, ref.Number, ws.Manager)
		if err := touchChanged(tx, res, err, port.TopicPRs|port.TopicFollow, ref); err != nil {
			return err
		}
		return nil
	})
	return err
}

// ReplaceWorkspaces makes links the set of linked pull requests in one
// transaction, so readers never see the links blank. It writes only the links
// that differ from the stored ones, recording on the host first the
// followed or unfollowed events of the scope flips.
func (s *Store) ReplaceWorkspaces(ctx context.Context, links map[github.Ref]dinv.Workspace) error {
	linked, err := workspaceLinks(ctx, s.db.R())
	if err != nil {
		return err
	}
	drop, set := linkChanges(linked, links)
	touched := append(drop, set...)
	if len(touched) == 0 {
		return nil
	}
	if err := s.recordFlipsOf(ctx, touched, time.Time{}, func(ref github.Ref, was scope) (scope, bool, error) {
		return was.with(was.reasons, links[ref].ID), true, nil
	}); err != nil {
		return err
	}
	_, err = s.db.Write(ctx, func(tx *sqlite.Tx) error {
		linked, err := workspaceLinks(ctx, tx)
		if err != nil {
			return err
		}
		drop, set := linkChanges(linked, links)
		if len(drop)+len(set) == 0 {
			return nil
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
		return nil
	})
	return err
}

// linkChanges are the linked pull requests links unlinks and those whose
// link it sets or changes.
func linkChanges(linked map[github.Ref]linkColumns, links map[github.Ref]dinv.Workspace) (drop, set []github.Ref) {
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
	return drop, set
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
func workspaceLinks(ctx context.Context, tx sqlite.Reader) (map[github.Ref]linkColumns, error) {
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
// first, then the overrides of those that have one.
func (s *Store) PRsWithOverrides(ctx context.Context) ([]dinv.PR, map[github.Ref]dinv.Override, error) {
	prs, err := s.ListPRs(ctx, false)
	if err != nil {
		return nil, nil, err
	}
	all, err := s.Overrides(ctx)
	if err != nil {
		return nil, nil, err
	}
	overrides := map[github.Ref]dinv.Override{}
	for _, p := range prs {
		if o, ok := all[p.Ref]; ok {
			overrides[p.Ref] = o
		}
	}
	return prs, overrides, nil
}

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

// timed refuses events with no time.
func timed(evs []dinv.Event) error {
	for _, e := range evs {
		if e.At.IsZero() {
			return fmt.Errorf("event %s on %s/%s#%d has no time", e.Kind, e.PR.Owner, e.PR.Repo, e.PR.Number)
		}
	}
	return nil
}

// SetTracked tracks or untracks ref, syncing the pull request's reasons in
// the same transaction; changed says the override moved.
func (s *Store) SetTracked(ctx context.Context, ref github.Ref, tracked bool) (changed bool, err error) {
	o, err := s.Override(ctx, ref)
	if err != nil {
		return false, err
	}
	return s.writeOverride(ctx, ref, o.Adopted, tracked)
}

// writeOverride writes ref's plugin columns of overrides, recording on the
// host first the followed or unfollowed event of a scope flip; written
// says a column moved.
func (s *Store) writeOverride(ctx context.Context, ref github.Ref, adopted, tracked bool) (written bool, err error) {
	if err := s.recordFlipsOf(ctx, []github.Ref{ref}, time.Time{}, func(_ github.Ref, sc scope) (scope, bool, error) {
		return sc.with(withTracked(sc.reasons, tracked), sc.ws), true, nil
	}); err != nil {
		return false, err
	}
	_, err = s.db.Write(ctx, func(tx *sqlite.Tx) error {
		if !adopted && !tracked {
			res, err := tx.ExecContext(ctx, `UPDATE overrides SET taken_over=0, tracked=0 WHERE provider=? AND unit_key=? AND (taken_over, tracked) IS NOT (0, 0)`,
				unitArgs(ref)...)
			if err != nil {
				return err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return err
			}
			if written = n > 0; written {
				tx.Touch(port.TopicOverrides, ref.Unit())
			}
			return syncTracked(ctx, tx, ref, tracked)
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO overrides (provider, unit_key, taken_over, tracked) VALUES (?,?,?,?)
		 ON CONFLICT(provider, unit_key) DO UPDATE SET taken_over=excluded.taken_over, tracked=excluded.tracked
		 WHERE (taken_over, tracked) IS NOT (excluded.taken_over, excluded.tracked)`, append(unitArgs(ref), adopted, tracked)...)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if written = n > 0; written {
			tx.Touch(port.TopicOverrides, ref.Unit())
		}
		return syncTracked(ctx, tx, ref, tracked)
	})
	return written, err
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

func (s *Store) PRsByUnit(ctx context.Context, units []unit.Unit) (map[unit.Unit]dinv.PR, error) {
	refs := make([]github.Ref, 0, len(units))
	for _, u := range units {
		if ref, ok := pr.RefOf(u); ok {
			refs = append(refs, ref)
		}
	}
	got, err := s.PRs(ctx, refs)
	if err != nil {
		return nil, err
	}
	out := make(map[unit.Unit]dinv.PR, len(got))
	for ref, p := range got {
		out[ref.Unit()] = p
	}
	return out, nil
}

// recordEvents records evs on the host through the broker.
func (s *Store) recordEvents(ctx context.Context, evs []dinv.Event) error {
	if len(evs) == 0 {
		return nil
	}
	if err := timed(evs); err != nil {
		return err
	}
	wire := make([]src.Event, len(evs))
	for i, e := range evs {
		wire[i] = wireEvent(e)
	}
	return s.Store.Host().RecordEvents(ctx, wire)
}

// hostEvents reads the host's events of refs of kinds, every kind when
// empty, through the broker.
func (s *Store) hostEvents(ctx context.Context, refs []github.Ref, kinds ...string) ([]dinv.Event, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	units := make([]plugin.Unit, len(refs))
	for i, r := range refs {
		u := r.Unit()
		units[i] = plugin.Unit{Provider: string(u.Provider), Key: u.Key}
	}
	got, err := s.Store.Host().Events(ctx, src.EventQuery{Units: units, Kinds: kinds})
	if err != nil {
		return nil, err
	}
	out := make([]dinv.Event, len(got))
	for i, e := range got {
		out[i] = dinv.Event{PR: refOf(e.Unit.Provider, e.Unit.Key), Kind: e.Kind, Key: e.Key, Actor: e.Actor, At: e.At, Data: string(e.Data)}
	}
	return out, nil
}

// SetOverride writes o whole: the host's columns through the host's store,
// then the plugin's with the pull request's reasons, each in its own
// transaction.
func (s *Store) SetOverride(ctx context.Context, o dinv.Override) error {
	if _, err := s.db.SetOverride(ctx, o.PR.Unit(), hostOverride(o), nil); err != nil {
		return err
	}
	_, err := s.writeOverride(ctx, o.PR, o.Adopted, o.Tracked)
	return err
}

// hostOverride are o's host columns by name, its next guidance left alone.
func hostOverride(o dinv.Override) map[string]string {
	set := map[string]string{port.OverrideHold: ""}
	if o.Hold {
		set[port.OverrideHold] = port.OverrideTrue
	}
	return set
}

func wireEvent(e dinv.Event) src.Event {
	u := e.PR.Unit()
	out := src.Event{Unit: plugin.Unit{Provider: string(u.Provider), Key: u.Key}, Kind: e.Kind, Key: e.Key, Actor: e.Actor, At: e.At}
	if e.Data != "" {
		out.Data = json.RawMessage(e.Data)
	}
	return out
}
