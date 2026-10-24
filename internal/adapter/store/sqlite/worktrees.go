package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
)

// Worktrees is the store of worktree custody.
type Worktrees struct{ db *DB }

func (db *DB) Worktrees() Worktrees { return Worktrees{db} }

// Worktree is the custody of the worktree at path.
func (w Worktrees) Worktree(ctx context.Context, path string) (wsdomain.Custody, bool, error) {
	var c wsdomain.Custody
	var at string
	err := w.db.R().QueryRowContext(ctx, `SELECT path, repo, manager, workspace_id, created_by, at FROM worktrees WHERE path = ?`, path).
		Scan(&c.Path, &c.Repo, &c.Manager, &c.WorkspaceID, &c.CreatedBy, &at)
	if err == sql.ErrNoRows {
		return wsdomain.Custody{}, false, nil
	}
	if err != nil {
		return wsdomain.Custody{}, false, err
	}
	c.At = parseTime(at)
	units, err := w.units(ctx, path)
	c.Units = units
	return c, err == nil, err
}

// Worktrees is every worktree in custody, by path.
func (w Worktrees) Worktrees(ctx context.Context) ([]wsdomain.Custody, error) {
	rows, err := w.db.R().QueryContext(ctx, `SELECT path, repo, manager, workspace_id, created_by, at FROM worktrees ORDER BY path`)
	if err != nil {
		return nil, err
	}
	var out []wsdomain.Custody
	for rows.Next() {
		var c wsdomain.Custody
		var at string
		if err := rows.Scan(&c.Path, &c.Repo, &c.Manager, &c.WorkspaceID, &c.CreatedBy, &at); err != nil {
			rows.Close()
			return nil, err
		}
		c.At = parseTime(at)
		out = append(out, c)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Units, err = w.units(ctx, out[i].Path); err != nil {
			return nil, err
		}
	}
	return out, rows.Err()
}

func (w Worktrees) units(ctx context.Context, path string) ([]unit.Unit, error) {
	rows, err := w.db.R().QueryContext(ctx, `SELECT provider, unit_key FROM worktree_units WHERE path = ? ORDER BY at, provider, unit_key`, path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []unit.Unit
	for rows.Next() {
		var u unit.Unit
		if err := rows.Scan(&u.Provider, &u.Key); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// Keep records c's owner; a worktree already in custody keeps its creator and
// takes c's owner, workspace id and repository when set.
func (w Worktrees) Keep(ctx context.Context, c wsdomain.Custody) error {
	at := c.At
	if at.IsZero() {
		at = time.Now()
	}
	_, err := w.db.Write(ctx, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO worktrees (path, repo, manager, workspace_id, created_by, at) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (path) DO UPDATE SET repo = iif(excluded.repo = '', repo, excluded.repo),
			manager = iif(excluded.manager = '', manager, excluded.manager),
			workspace_id = iif(excluded.workspace_id = '', workspace_id, excluded.workspace_id),
			created_by = iif(created_by = '', excluded.created_by, created_by)`,
			c.Path, c.Repo, c.Manager, c.WorkspaceID, c.CreatedBy, formatTime(at))
		if err != nil {
			return err
		}
		for _, u := range c.Units {
			if err := linkWorktreeUnit(ctx, tx, c.Path, u, at); err != nil {
				return err
			}
		}
		tx.Touch(port.TopicPRs, wsdomain.UnitOf(c.Path))
		return nil
	})
	return err
}

// LinkUnit records that the worktree at path stands for u.
func (w Worktrees) LinkUnit(ctx context.Context, path string, u unit.Unit) error {
	_, err := w.db.Write(ctx, func(tx *Tx) error {
		if err := linkWorktreeUnit(ctx, tx, path, u, time.Now()); err != nil {
			return err
		}
		tx.Touch(port.TopicPRs, wsdomain.UnitOf(path))
		return nil
	})
	return err
}

func linkWorktreeUnit(ctx context.Context, tx *Tx, path string, u unit.Unit, at time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO worktree_units (path, provider, unit_key, at) SELECT ?, ?, ?, ?
		WHERE EXISTS (SELECT 1 FROM worktrees WHERE path = ?) ON CONFLICT DO NOTHING`, path, string(u.Provider), u.Key, formatTime(at), path)
	return err
}

// Drop forgets the worktree at path and its units.
func (w Worktrees) Drop(ctx context.Context, path string) error {
	_, err := w.db.Write(ctx, func(tx *Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM worktree_units WHERE path = ?`, path); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM worktrees WHERE path = ?`, path)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			tx.Touch(port.TopicPRs, wsdomain.UnitOf(path))
		}
		return nil
	})
	return err
}
