package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/plugins/github/pr"
)

const (
	prReadChunk = 400
	seeChunk    = 500
)

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

// refValues is the VALUES rows and arguments of refs, for an IN on the
// primary key.
func refValues(refs []pr.Ref) (string, []any) {
	args := make([]any, 0, 3*len(refs))
	for _, r := range refs {
		args = append(args, r.Owner, r.Repo, r.Number)
	}
	return strings.TrimSuffix(strings.Repeat("(?,?,?),", len(refs)), ","), args
}

// refsIn is query, which ends in the primary key's columns, followed by an
// IN of the VALUES rows marks.
func refsIn(query, marks string) string { return query + ` IN (VALUES ` + marks + `)` }

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

// UpsertPR stores p as SavePRs does, seen at its LastSeenAt.
func (db *DB) UpsertPR(ctx context.Context, p pr.PR) error {
	return db.SavePRs(ctx, []pr.PR{p}, p.LastSeenAt)
}

// stored is what saving a pull request reads of its stored row.
type stored struct {
	reasons                string
	mergeable, mergeableAt string
	tracked                bool
}

// SavePRs stores prs in one transaction, rewriting a row only when a column
// differs, and records each as seen at seenAt. A row's reasons are the
// union of the stored ones and its own, with ReasonTracked exactly while
// its override tracks it; seenAt dates a new conflict.
func (db *DB) SavePRs(ctx context.Context, prs []pr.PR, seenAt time.Time) error {
	if len(prs) == 0 {
		return nil
	}
	refs := make([]pr.Ref, len(prs))
	for i, p := range prs {
		refs[i] = p.Ref
	}
	_, err := db.Write(ctx, func(tx *Tx) error {
		prior, err := storedRows(ctx, tx, refs)
		if err != nil {
			return err
		}
		upsert, err := tx.PrepareContext(ctx, upsertPRSQL)
		if err != nil {
			return err
		}
		defer upsert.Close()
		for _, p := range prs {
			was := prior[p.Ref]
			mergeable, mergeableAt := nextMergeable(was.mergeable, was.mergeableAt, p.Mergeable, seenAt)
			storedReasons, err := splitList(was.reasons)
			if err != nil {
				return err
			}
			reasons := joinList(withTracked(append(slices.Clone(p.Reasons), storedReasons...), was.tracked))
			failing := ""
			if len(p.FailingChecks) > 0 {
				raw, err := json.Marshal(p.FailingChecks)
				if err != nil {
					return err
				}
				failing = string(raw)
			}
			res, err := upsert.ExecContext(ctx, p.Ref.Owner, p.Ref.Repo, p.Ref.Number, p.Title, p.Author, p.State, p.IsDraft, p.HeadOid, p.HeadBranch, p.BaseBranch,
				p.IsFork, p.ChecksState, p.ReviewDecision, joinList(p.ReviewRequests), p.URL, formatTime(p.CreatedAt), formatTime(p.UpdatedAt),
				formatTime(p.MergedAt), formatTime(p.ClosedAt), reasons, formatTime(p.BodyEditedAt), p.ChangedFiles, failing, mergeable, mergeableAt)
			if err := tx.touchChanged(res, err, p.Ref); err != nil {
				return err
			}
			prior[p.Ref] = stored{reasons: reasons, mergeable: mergeable, mergeableAt: mergeableAt, tracked: was.tracked}
		}
		return seeRefs(ctx, tx, refs, seenAt)
	})
	return err
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
	return next, formatTime(at)
}

// storedRows reads what saving needs of each of refs, absent ones included.
func storedRows(ctx context.Context, tx reader, refs []pr.Ref) (map[pr.Ref]stored, error) {
	out := make(map[pr.Ref]stored, len(refs))
	for chunk := range slices.Chunk(refs, prReadChunk) {
		marks, args := refValues(chunk)
		rows, err := tx.QueryContext(ctx, `WITH r(owner, repo, number) AS (VALUES `+marks+`)
			SELECT r.owner, r.repo, r.number, coalesce(p.reasons, ''), coalesce(p.mergeable, ''), coalesce(p.mergeable_at, ''), coalesce(o.tracked, 0)
			FROM r LEFT JOIN pull_requests p ON p.owner = r.owner AND p.repo = r.repo AND p.number = r.number
			LEFT JOIN overrides o ON o.unit_key = r.owner || '/' || r.repo || '#' || r.number`, args...)
		if err != nil {
			return nil, err
		}
		err = func() error {
			defer rows.Close()
			for rows.Next() {
				var ref pr.Ref
				var st stored
				if err := rows.Scan(&ref.Owner, &ref.Repo, &ref.Number, &st.reasons, &st.mergeable, &st.mergeableAt, &st.tracked); err != nil {
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

// withTracked puts ReasonTracked in reasons exactly when tracked, sorted.
func withTracked(reasons []string, tracked bool) []string {
	out := slices.DeleteFunc(slices.Clone(reasons), func(r string) bool { return r == pr.ReasonTracked })
	if tracked {
		out = append(out, pr.ReasonTracked)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// SetReasons replaces ref's follow reasons, keeping ReasonTracked while its
// override tracks it.
func (db *DB) SetReasons(ctx context.Context, ref pr.Ref, reasons []string) error {
	_, err := db.Write(ctx, func(tx *Tx) error {
		tracked, err := isTracked(ctx, tx, ref)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE pull_requests SET reasons=? WHERE owner=? AND repo=? AND number=? AND reasons IS NOT ?1`,
			joinList(withTracked(reasons, tracked)), ref.Owner, ref.Repo, ref.Number)
		return tx.touchChanged(res, err, ref)
	})
	return err
}

// seeRefs dates the sighting of the stored refs among refs at at, one
// statement per seeChunk refs.
func seeRefs(ctx context.Context, tx *Tx, refs []pr.Ref, at time.Time) error {
	for chunk := range slices.Chunk(refs, seeChunk) {
		marks, args := refValues(chunk)
		if _, err := tx.ExecContext(ctx, `INSERT INTO pr_seen (owner, repo, number, at)
			SELECT owner, repo, number, ? FROM pull_requests WHERE (owner, repo, number) IN (VALUES `+marks+`) ON CONFLICT DO UPDATE SET at = excluded.at WHERE at != excluded.at`,
			append([]any{formatTime(at)}, args...)...); err != nil {
			return err
		}
	}
	return nil
}

// SeeRefs records that a search or a read returned refs at at. A sighting
// changes no reported fact.
func (db *DB) SeeRefs(ctx context.Context, refs []pr.Ref, at time.Time) error {
	if len(refs) == 0 {
		return nil
	}
	_, err := db.Write(ctx, func(tx *Tx) error { return seeRefs(ctx, tx, refs, at) })
	return err
}

// Seen is when a search or a read last returned each open pull request.
func (db *DB) Seen(ctx context.Context) (map[pr.Ref]time.Time, error) {
	rows, err := db.db.QueryContext(ctx, `SELECT p.owner, p.repo, p.number, COALESCE(s.at, '') FROM pull_requests p LEFT JOIN pr_seen s USING (owner, repo, number) WHERE p.state = ?`, pr.StateOpen)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[pr.Ref]time.Time{}
	for rows.Next() {
		var ref pr.Ref
		var raw string
		if err := rows.Scan(&ref.Owner, &ref.Repo, &ref.Number, &raw); err != nil {
			return nil, err
		}
		at, err := parseTime(raw)
		if err != nil {
			return nil, err
		}
		out[ref] = at
	}
	return out, rows.Err()
}

const prColumns = `pr.owner, pr.repo, pr.number, pr.title, pr.author, pr.state, pr.is_draft, pr.head_oid, pr.head_branch, pr.base_branch,
 pr.is_fork, pr.checks_state, pr.review_decision, pr.review_requests, pr.url, pr.created_at, pr.updated_at, pr.merged_at, pr.closed_at,
 COALESCE((SELECT at FROM pr_seen WHERE owner = pr.owner AND repo = pr.repo AND number = pr.number), ''),
 pr.reasons, pr.ws_id, pr.ws_name, pr.ws_status, pr.ws_review, pr.ws_agent, pr.ws_comment, pr.ws_main, pr.ws_manager,
 pr.body_edited_at, pr.changed_files, pr.failing_checks, pr.mergeable, pr.mergeable_at`

func scanPR(row scanner) (pr.PR, error) {
	var p pr.PR
	var requests, created, updated, merged, closed, seen, reasons, agent, edited, failing, conflictAt string
	err := row.Scan(&p.Ref.Owner, &p.Ref.Repo, &p.Ref.Number, &p.Title, &p.Author, &p.State, &p.IsDraft, &p.HeadOid,
		&p.HeadBranch, &p.BaseBranch, &p.IsFork, &p.ChecksState, &p.ReviewDecision, &requests, &p.URL,
		&created, &updated, &merged, &closed, &seen, &reasons,
		&p.Workspace.ID, &p.Workspace.DisplayName, &p.Workspace.Status, &p.Workspace.IsReview, &agent, &p.Workspace.Comment, &p.Workspace.IsMain,
		&p.Workspace.Manager, &edited, &p.ChangedFiles, &failing, &p.Mergeable, &conflictAt)
	if err != nil {
		return pr.PR{}, err
	}
	// Unreadable names cost only the names: the next read of the pull
	// request rewrites them.
	if failing != "" && json.Unmarshal([]byte(failing), &p.FailingChecks) != nil {
		p.FailingChecks = nil
	}
	for _, l := range []struct {
		dst *[]string
		src string
	}{{&p.ReviewRequests, requests}, {&p.Reasons, reasons}, {&p.Workspace.AgentStates, agent}} {
		if *l.dst, err = splitList(l.src); err != nil {
			return pr.PR{}, err
		}
	}
	for _, t := range []struct {
		dst *time.Time
		src string
	}{{&p.CreatedAt, created}, {&p.UpdatedAt, updated}, {&p.MergedAt, merged}, {&p.ClosedAt, closed}, {&p.LastSeenAt, seen}, {&p.BodyEditedAt, edited}, {&p.MergeableAt, conflictAt}} {
		if *t.dst, err = parseTime(t.src); err != nil {
			return pr.PR{}, err
		}
	}
	return p, nil
}

// PR is the stored pull request ref; false when the store lacks it.
func (db *DB) PR(ctx context.Context, ref pr.Ref) (pr.PR, bool, error) {
	p, err := scanPR(db.db.QueryRowContext(ctx, `SELECT `+prColumns+` FROM pull_requests pr WHERE pr.owner=? AND pr.repo=? AND pr.number=?`, ref.Owner, ref.Repo, ref.Number))
	if noRows(err) {
		return pr.PR{}, false, nil
	}
	if err != nil {
		return pr.PR{}, false, err
	}
	return p, true, nil
}

// PRs reads the stored pull requests among refs, leaving out those it lacks.
func (db *DB) PRs(ctx context.Context, refs []pr.Ref) (map[pr.Ref]pr.PR, error) {
	out := make(map[pr.Ref]pr.PR, len(refs))
	for chunk := range slices.Chunk(refs, prReadChunk) {
		marks, args := refValues(chunk)
		rows, err := db.db.QueryContext(ctx, refsIn(`SELECT `+prColumns+` FROM pull_requests pr WHERE (pr.owner, pr.repo, pr.number)`, marks), args...)
		if err != nil {
			return nil, err
		}
		list, err := scanPRs(rows)
		if err != nil {
			return nil, err
		}
		for _, p := range list {
			out[p.Ref] = p
		}
	}
	return out, nil
}

// ListPRs reads every stored pull request, or the open ones, the most
// recently updated first.
func (db *DB) ListPRs(ctx context.Context, open bool) ([]pr.PR, error) {
	query := `SELECT ` + prColumns + ` FROM pull_requests pr`
	if open {
		query += ` WHERE pr.state='OPEN'`
	}
	rows, err := db.db.QueryContext(ctx, query+` ORDER BY pr.updated_at DESC, pr.owner, pr.repo, pr.number DESC`)
	if err != nil {
		return nil, err
	}
	return scanPRs(rows)
}

func scanPRs(rows *sql.Rows) ([]pr.PR, error) {
	defer rows.Close()
	var out []pr.PR
	for rows.Next() {
		p, err := scanPR(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
