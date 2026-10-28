package sqlite

import "context"

// DossierMoves are the dossier directories a merge of tasks moved in the store and that still wait to move
// on disk, from the path under the dossiers root to the path they go to.
func (db *DB) DossierMoves(ctx context.Context) (map[string]string, error) {
	rows, err := db.R().QueryContext(ctx, `SELECT from_path, to_path FROM dossier_moves`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var from, to string
		if err := rows.Scan(&from, &to); err != nil {
			return nil, err
		}
		out[from] = to
	}
	return out, rows.Err()
}

// DossierMoved forgets the move of the dossier directory at from, done on disk.
func (db *DB) DossierMoved(ctx context.Context, from string) error {
	_, err := db.Write(ctx, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM dossier_moves WHERE from_path = ?`, from)
		return err
	})
	return err
}
