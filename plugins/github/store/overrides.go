package store

import (
	"context"
	"slices"

	"github.com/wloophq/wloop/plugins/github/pr"
)

func isTracked(ctx context.Context, tx reader, ref pr.Ref) (bool, error) {
	var tracked bool
	err := tx.QueryRowContext(ctx, `SELECT tracked FROM overrides WHERE unit_key=?`, ref.String()).Scan(&tracked)
	if noRows(err) {
		return false, nil
	}
	return tracked, err
}

// Override is what the user set on ref; the zero override when none.
func (db *DB) Override(ctx context.Context, ref pr.Ref) (pr.Override, error) {
	o := pr.Override{Ref: ref}
	err := db.db.QueryRowContext(ctx, `SELECT taken_over, tracked FROM overrides WHERE unit_key=?`, ref.String()).Scan(&o.Adopted, &o.Tracked)
	if noRows(err) {
		return o, nil
	}
	return o, err
}

// SetTracked tracks or untracks ref, keeping its reasons in step; changed
// says the override moved.
func (db *DB) SetTracked(ctx context.Context, ref pr.Ref, tracked bool) (changed bool, err error) {
	_, err = db.Write(ctx, func(tx *Tx) error {
		query := `UPDATE overrides SET tracked=0 WHERE unit_key=? AND tracked`
		if tracked {
			query = `INSERT INTO overrides (unit_key, tracked) VALUES (?, 1) ON CONFLICT(unit_key) DO UPDATE SET tracked=1 WHERE NOT tracked`
		}
		res, err := tx.ExecContext(ctx, query, ref.String())
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if changed = n > 0; changed {
			tx.Touch(ref)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM overrides WHERE unit_key=? AND NOT taken_over AND NOT tracked`, ref.String()); err != nil {
			return err
		}
		return syncTracked(ctx, tx, ref, tracked)
	})
	return changed, err
}

// syncTracked makes a stored pull request's reasons follow its override; one
// not stored yet gets the reason when it is first saved.
func syncTracked(ctx context.Context, tx *Tx, ref pr.Ref, tracked bool) error {
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT reasons FROM pull_requests WHERE owner=? AND repo=? AND number=?`, ref.Owner, ref.Repo, ref.Number).Scan(&raw)
	if noRows(err) {
		return nil
	}
	if err != nil {
		return err
	}
	reasons, err := splitList(raw)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE pull_requests SET reasons=? WHERE owner=? AND repo=? AND number=? AND reasons IS NOT ?1`,
		joinList(withTracked(reasons, tracked)), ref.Owner, ref.Repo, ref.Number)
	return tx.touchChanged(res, err, ref)
}

// SetAdopted marks ref taken over, writing that column alone.
func (db *DB) SetAdopted(ctx context.Context, ref pr.Ref) error {
	_, err := db.Write(ctx, func(tx *Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO overrides (unit_key, taken_over) VALUES (?, 1)
			ON CONFLICT(unit_key) DO UPDATE SET taken_over=1 WHERE taken_over IS NOT 1`, ref.String())
		return tx.touchChanged(res, err, ref)
	})
	return err
}

// Tracked lists the pull requests the user tracks, in ref order.
func (db *DB) Tracked(ctx context.Context) ([]pr.Ref, error) {
	rows, err := db.db.QueryContext(ctx, `SELECT unit_key FROM overrides WHERE tracked`)
	if err != nil {
		return nil, err
	}
	keys, err := scanStrings(rows)
	if err != nil {
		return nil, err
	}
	out := make([]pr.Ref, 0, len(keys))
	for _, k := range keys {
		ref, err := pr.ParseKey(k)
		if err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	slices.SortFunc(out, pr.Compare)
	return out, nil
}
