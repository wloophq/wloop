package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wloophq/wloop/internal/domain/dossier"
)

// v6Project is the owner prefix version 6 gave a project's dossier, its slug after it.
const v6Project = "project"

// v7Dossiers gives every dossier to the task that owns it in version 7: a project's, found by its slug, to the
// project's task, and a unit's to the task holding the unit (an open one first, then the oldest), as
// dossierStore.Owner finds it. Each directory move is recorded in dossier_moves for the CLI to apply on disk.
// projects, which kept only the slugs, goes.
func v7Dossiers(ctx context.Context, tx *sql.Tx) error {
	type give struct{ from, to string }
	var gives []give
	err := eachRow(ctx, tx, `SELECT d.owner, p.task_id FROM projects p JOIN dossiers d ON d.owner = ?1 || '/' || p.slug
		WHERE p.slug != '' ORDER BY d.owner`, []any{v6Project}, func(r *sql.Rows) error {
		var g give
		if err := r.Scan(&g.from, &g.to); err != nil {
			return err
		}
		gives = append(gives, g)
		return nil
	})
	if err != nil {
		return err
	}
	var units []string
	err = eachRow(ctx, tx, `SELECT owner FROM dossiers WHERE owner NOT LIKE ?1 || '/%' AND owner NOT LIKE ?2 || '/%'
		AND owner NOT LIKE ?3 || '/%' ORDER BY owner`, []any{v7Task, v6Project, dossier.MergedDir}, func(r *sql.Rows) error {
		var owner string
		if err := r.Scan(&owner); err != nil {
			return err
		}
		units = append(units, owner)
		return nil
	})
	if err != nil {
		return err
	}
	for _, owner := range units {
		o, err := dossier.ParseOwner(owner)
		if err != nil {
			return fmt.Errorf("the dossier %s: %w", owner, err)
		}
		var id string
		err = tx.QueryRowContext(ctx, `SELECT e.id FROM tasks e WHERE e.id IN (
			SELECT task_id FROM task_units WHERE provider = ?1 AND unit_key = ?2
			UNION SELECT task_id FROM task_items WHERE provider = ?1 AND unit_key = ?2)
			ORDER BY e.category IN ('done', 'dropped'), e.created, e.id LIMIT 1`, string(o.Unit.Provider), o.Unit.Key).Scan(&id)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			continue
		case err != nil:
			return err
		}
		gives = append(gives, give{owner, id})
	}
	for _, g := range gives {
		if err := giveDossier(ctx, tx, g.from, g.from, g.to); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `DROP TABLE projects`)
	return err
}
