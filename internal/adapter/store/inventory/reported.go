package inventory

import (
	"context"
	"encoding/json"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/port"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/github"
)

// Reported is a pull request as the GitHub plugin reported it: its row,
// with the reasons, workspace link and sighting the plugin derived, its
// detail, and whether the user tracks it.
type Reported struct {
	PR dinv.PR
	// Detail nil keeps the stored one.
	Detail  *Detail
	Tracked bool
	// Adopted says the plugin keeps the pull request taken over.
	Adopted bool
}

const reportedPRSQL = `INSERT INTO pull_requests
 (owner, repo, number, title, author, state, is_draft, head_oid, head_branch, base_branch, is_fork, checks_state, review_decision, review_requests, url,
  created_at, updated_at, merged_at, closed_at, reasons, body_edited_at, changed_files, failing_checks, mergeable, mergeable_at,
  ws_id, ws_name, ws_status, ws_review, ws_agent, ws_comment, ws_main, ws_manager)
 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT(owner, repo, number) DO UPDATE SET title=excluded.title, author=excluded.author, state=excluded.state,
 is_draft=excluded.is_draft, head_oid=excluded.head_oid, head_branch=excluded.head_branch, base_branch=excluded.base_branch,
 is_fork=excluded.is_fork, checks_state=excluded.checks_state, review_decision=excluded.review_decision, review_requests=excluded.review_requests,
 url=excluded.url, created_at=excluded.created_at, updated_at=excluded.updated_at, merged_at=excluded.merged_at, closed_at=excluded.closed_at,
 reasons=excluded.reasons, body_edited_at=excluded.body_edited_at, changed_files=excluded.changed_files, failing_checks=excluded.failing_checks,
 mergeable=excluded.mergeable, mergeable_at=excluded.mergeable_at, ws_id=excluded.ws_id, ws_name=excluded.ws_name, ws_status=excluded.ws_status,
 ws_review=excluded.ws_review, ws_agent=excluded.ws_agent, ws_comment=excluded.ws_comment, ws_main=excluded.ws_main, ws_manager=excluded.ws_manager
 WHERE (title, author, state, is_draft, head_oid, head_branch, base_branch, is_fork, checks_state, review_decision, review_requests, url, created_at,
  updated_at, merged_at, closed_at, reasons, body_edited_at, changed_files, failing_checks, mergeable, mergeable_at,
  ws_id, ws_name, ws_status, ws_review, ws_agent, ws_comment, ws_main, ws_manager)
 IS NOT (excluded.title, excluded.author, excluded.state, excluded.is_draft, excluded.head_oid, excluded.head_branch, excluded.base_branch,
  excluded.is_fork, excluded.checks_state, excluded.review_decision, excluded.review_requests, excluded.url, excluded.created_at, excluded.updated_at,
  excluded.merged_at, excluded.closed_at, excluded.reasons, excluded.body_edited_at, excluded.changed_files, excluded.failing_checks, excluded.mergeable,
  excluded.mergeable_at, excluded.ws_id, excluded.ws_name, excluded.ws_status, excluded.ws_review, excluded.ws_agent, excluded.ws_comment,
  excluded.ws_main, excluded.ws_manager)`

const (
	reportedTrackSQL = `INSERT INTO overrides (provider, unit_key, tracked) VALUES (?,?,1)
 ON CONFLICT(provider, unit_key) DO UPDATE SET tracked=1 WHERE tracked IS NOT 1`
	reportedUntrackSQL = `UPDATE overrides SET tracked=0 WHERE provider=? AND unit_key=? AND tracked`
	reportedAdoptSQL   = `INSERT INTO overrides (provider, unit_key, taken_over) VALUES (?,?,1)
 ON CONFLICT(provider, unit_key) DO UPDATE SET taken_over=1 WHERE taken_over IS NOT 1`
)

// SaveReported keeps rows as the plugin reported them, in one transaction:
// each row whole, its sighting, its detail, its track and its adoption,
// which only the plugin keeps from now on, writing only what
// differs. It derives and records no event: the plugin recorded its events
// on the host before it reported.
func (s *Store) SaveReported(ctx context.Context, rows []Reported) error {
	if len(rows) == 0 {
		return nil
	}
	_, err := s.db.Write(ctx, func(tx *sqlite.Tx) error {
		upsert, err := tx.PrepareContext(ctx, reportedPRSQL)
		if err != nil {
			return err
		}
		defer upsert.Close()
		for _, r := range rows {
			p, ws := r.PR, r.PR.Workspace
			failing := ""
			if len(p.FailingChecks) > 0 {
				raw, err := json.Marshal(p.FailingChecks)
				if err != nil {
					return err
				}
				failing = string(raw)
			}
			res, err := upsert.ExecContext(ctx, p.Ref.Owner, p.Ref.Repo, p.Ref.Number, p.Title, p.Author, p.State, p.IsDraft, p.HeadOid, p.HeadBranch,
				p.BaseBranch, p.IsFork, p.ChecksState, p.ReviewDecision, joinList(p.ReviewRequests), p.URL, ts(p.CreatedAt), ts(p.UpdatedAt),
				ts(p.MergedAt), ts(p.ClosedAt), joinList(p.Reasons), ts(p.BodyEditedAt), p.ChangedFiles, failing, p.Mergeable, ts(p.MergeableAt),
				ws.ID, ws.DisplayName, ws.Status, ws.IsReview, joinStates(ws.AgentStates), ws.Comment, ws.IsMain, ws.Manager)
			if err := touchChanged(tx, res, err, port.TopicPRs|port.TopicFollow, p.Ref); err != nil {
				return err
			}
			if !p.LastSeenAt.IsZero() {
				if err := seeRefs(ctx, tx, []github.Ref{p.Ref}, p.LastSeenAt); err != nil {
					return err
				}
			}
			if r.Detail != nil {
				was, err := storedRows(ctx, tx, []github.Ref{p.Ref})
				if err != nil {
					return err
				}
				if _, err := saveDetail(ctx, tx, p.Ref, *r.Detail, was[p.Ref].detail, r.Detail.ReadAt); err != nil {
					return err
				}
			}
			track := reportedUntrackSQL
			if r.Tracked {
				track = reportedTrackSQL
			}
			res, err = tx.ExecContext(ctx, track, unitArgs(p.Ref)...)
			if err := touchChanged(tx, res, err, port.TopicOverrides, p.Ref); err != nil {
				return err
			}
			if r.Adopted {
				res, err = tx.ExecContext(ctx, reportedAdoptSQL, unitArgs(p.Ref)...)
				if err := touchChanged(tx, res, err, port.TopicOverrides, p.Ref); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return err
}
