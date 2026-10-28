package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/wloophq/wloop/internal/core/dossiers"
	"github.com/wloophq/wloop/internal/domain/dossier"
)

type dossierStore struct{ db *DB }

var _ dossiers.Store = dossierStore{}

// Dossiers is the dossiers table, and whether a dossier's unit still has open work.
func (db *DB) Dossiers() dossiers.Store { return dossierStore{db} }

const (
	dossierColumns = `owner, rel_path, status, bytes, files, last_write_at, resolved_at, archived_at, purge_after`
	putDossierSQL  = `INSERT INTO dossiers (` + dossierColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (owner) DO UPDATE SET rel_path = excluded.rel_path, status = excluded.status, bytes = excluded.bytes,
		files = excluded.files, last_write_at = excluded.last_write_at, resolved_at = excluded.resolved_at,
		archived_at = excluded.archived_at, purge_after = excluded.purge_after`
	unitOpenSQL = `SELECT EXISTS (SELECT 1 FROM items WHERE provider = ?1 AND unit_key = ?2 AND state = 'open')
		OR EXISTS (SELECT 1 FROM tasks e WHERE e.category NOT IN (?3, ?4) AND (
			e.id IN (SELECT task_id FROM task_items WHERE provider = ?1 AND unit_key = ?2) OR
			e.id IN (SELECT task_id FROM task_units WHERE provider = ?1 AND unit_key = ?2)))`
	projectOpenSQL = `SELECT EXISTS (SELECT 1 FROM projects p JOIN tasks e ON e.id = p.task_id
		WHERE p.slug = ?1 AND p.slug != '' AND e.category NOT IN (?2, ?3))`
)

func (s dossierStore) Put(ctx context.Context, d dossier.Dossier) error {
	if _, err := dossier.Rel(d.Owner); err != nil {
		return err
	}
	_, err := s.db.Write(ctx, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx, putDossierSQL, d.Owner.String(), d.RelPath, string(d.Status), d.Bytes, d.Files,
			dossierTime(d.LastWriteAt), dossierTime(d.ResolvedAt), dossierTime(d.ArchivedAt), dossierTime(d.PurgeAfter))
		return err
	})
	return err
}

func (s dossierStore) Get(ctx context.Context, o dossier.Owner) (dossier.Dossier, bool, error) {
	d, err := scanDossier(s.db.R().QueryRowContext(ctx, `SELECT `+dossierColumns+` FROM dossiers WHERE owner = ?`, o.String()))
	if errors.Is(err, sql.ErrNoRows) {
		return dossier.Dossier{}, false, nil
	}
	return d, err == nil, err
}

func (s dossierStore) List(ctx context.Context) ([]dossier.Dossier, error) {
	rows, err := s.db.R().QueryContext(ctx, `SELECT `+dossierColumns+` FROM dossiers ORDER BY owner`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dossier.Dossier
	for rows.Next() {
		d, err := scanDossier(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s dossierStore) Open(ctx context.Context, o dossier.Owner) (bool, error) {
	var open bool
	if o.Project != "" {
		err := s.db.R().QueryRowContext(ctx, projectOpenSQL, append([]any{o.Project}, openArgs()...)...).Scan(&open)
		return open, err
	}
	err := s.db.R().QueryRowContext(ctx, unitOpenSQL, append([]any{string(o.Unit.Provider), o.Unit.Key}, openArgs()...)...).Scan(&open)
	return open, err
}

func scanDossier(row scanner) (dossier.Dossier, error) {
	var d dossier.Dossier
	var owner, status, lastWrite, resolved, archived, purge string
	if err := row.Scan(&owner, &d.RelPath, &status, &d.Bytes, &d.Files, &lastWrite, &resolved, &archived, &purge); err != nil {
		return dossier.Dossier{}, err
	}
	o, err := dossier.ParseOwner(owner)
	if err != nil {
		return dossier.Dossier{}, err
	}
	d.Owner, d.Status = o, dossier.Status(status)
	for _, t := range []struct {
		dst *time.Time
		raw string
	}{{&d.LastWriteAt, lastWrite}, {&d.ResolvedAt, resolved}, {&d.ArchivedAt, archived}, {&d.PurgeAfter, purge}} {
		if t.raw == "" {
			continue
		}
		if *t.dst, err = time.Parse(TimeLayout, t.raw); err != nil {
			return dossier.Dossier{}, fmt.Errorf("dossier %s: %w", owner, err)
		}
	}
	return d, nil
}

func dossierTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(TimeLayout)
}

// AgentWorktree is a worktree an agent session of the loop ran in, with the agent's home.
type AgentWorktree struct{ Home, Worktree string }

// AgentWorktrees lists the distinct worktrees the loop's sessions ran in.
func (db *DB) AgentWorktrees(ctx context.Context) ([]AgentWorktree, error) {
	rows, err := db.R().QueryContext(ctx, `SELECT home, worktree FROM agent_sessions WHERE worktree != ''
		UNION SELECT home, worktree FROM sessions WHERE worktree != '' ORDER BY worktree, home`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentWorktree
	for rows.Next() {
		var w AgentWorktree
		if err := rows.Scan(&w.Home, &w.Worktree); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// OpenReadOnly opens the store at path for reads only, for a process that is not the store's
// writer; it creates nothing and refuses a store at another version.
func OpenReadOnly(path string) (*DB, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	if err := Check(path); err != nil {
		return nil, err
	}
	r, err := handle(path+readerDSN, 1)
	if err != nil {
		return nil, err
	}
	return &DB{w: r, r: r}, nil
}
