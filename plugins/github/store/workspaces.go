package store

import (
	"context"
	"time"

	"github.com/wloophq/wloop/plugins/github/pr"
)

const setWorkspaceSQL = `UPDATE pull_requests SET ws_id=?1, ws_name=?2, ws_status=?3, ws_review=?4, ws_agent=?5, ws_comment=?6, ws_main=?7, ws_manager=?11
 WHERE owner=?8 AND repo=?9 AND number=?10 AND (ws_id, ws_name, ws_status, ws_review, ws_agent, ws_comment, ws_main, ws_manager) IS NOT (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?11)`

func setWorkspace(ctx context.Context, tx *Tx, ref pr.Ref, ws pr.Workspace) error {
	res, err := tx.ExecContext(ctx, setWorkspaceSQL,
		ws.ID, ws.DisplayName, ws.Status, ws.IsReview, joinList(ws.AgentStates), ws.Comment, ws.IsMain, ref.Owner, ref.Repo, ref.Number, ws.Manager)
	return tx.touchChanged(res, err, ref)
}

// SetWorkspace links ref to ws, recording on the host first the followed
// or unfollowed event of a scope flip.
func (db *DB) SetWorkspace(ctx context.Context, ref pr.Ref, ws pr.Workspace) error {
	if err := db.recordFlipsOf(ctx, []pr.Ref{ref}, time.Time{}, func(_ pr.Ref, was scope) (scope, bool, error) {
		return was.with(was.reasons, ws.ID), true, nil
	}); err != nil {
		return err
	}
	_, err := db.Write(ctx, func(tx *Tx) error { return setWorkspace(ctx, tx, ref, ws) })
	return err
}

// ReplaceWorkspaces makes links the set of linked pull requests in one
// transaction, so readers never see the links blank. It writes only the
// links that differ from the stored ones, recording on the host first the
// followed or unfollowed events of the scope flips.
func (db *DB) ReplaceWorkspaces(ctx context.Context, links map[pr.Ref]pr.Workspace) error {
	linked, err := workspaceLinks(ctx, db.db)
	if err != nil {
		return err
	}
	drop, set := linkChanges(linked, links)
	touched := append(drop, set...)
	if len(touched) == 0 {
		return nil
	}
	if err := db.recordFlipsOf(ctx, touched, time.Time{}, func(ref pr.Ref, was scope) (scope, bool, error) {
		return was.with(was.reasons, links[ref].ID), true, nil
	}); err != nil {
		return err
	}
	_, err = db.Write(ctx, func(tx *Tx) error {
		linked, err := workspaceLinks(ctx, tx)
		if err != nil {
			return err
		}
		drop, set := linkChanges(linked, links)
		for _, ref := range drop {
			if err := setWorkspace(ctx, tx, ref, pr.Workspace{}); err != nil {
				return err
			}
		}
		for _, ref := range set {
			if err := setWorkspace(ctx, tx, ref, links[ref]); err != nil {
				return err
			}
		}
		return nil
	})
	return err
}

// linkChanges are the linked pull requests links unlinks and those whose
// link it sets or changes.
func linkChanges(linked map[pr.Ref]linkColumns, links map[pr.Ref]pr.Workspace) (drop, set []pr.Ref) {
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

func linkKey(ws pr.Workspace) linkColumns {
	return linkColumns{ws.ID, ws.DisplayName, ws.Status, ws.IsReview, joinList(ws.AgentStates), ws.Comment, ws.IsMain, ws.Manager}
}

// workspaceLinks reads the workspace columns of every linked pull request.
func workspaceLinks(ctx context.Context, tx reader) (map[pr.Ref]linkColumns, error) {
	rows, err := tx.QueryContext(ctx, `SELECT owner, repo, number, ws_id, ws_name, ws_status, ws_review, ws_agent, ws_comment, ws_main, ws_manager FROM pull_requests
		WHERE ws_id IS NOT '' OR ws_name IS NOT '' OR ws_status IS NOT '' OR ws_review OR ws_agent IS NOT '' OR ws_comment IS NOT '' OR ws_main OR ws_manager IS NOT ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[pr.Ref]linkColumns{}
	for rows.Next() {
		var ref pr.Ref
		var l linkColumns
		if err := rows.Scan(&ref.Owner, &ref.Repo, &ref.Number, &l.id, &l.name, &l.status, &l.review, &l.agent, &l.comment, &l.main, &l.manager); err != nil {
			return nil, err
		}
		out[ref] = l
	}
	return out, rows.Err()
}
