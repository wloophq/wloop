package sqlite

import "context"

// DossierMove is a dossier directory a merge of tasks moved in the store and that waits to move on disk,
// from the path under the dossiers root to the path it goes to. Seq is the order the moves apply in.
type DossierMove struct {
	Seq      int64
	From, To string
}

// DossierMoves are the dossier moves still to do on disk, in the order they apply.
func (db *DB) DossierMoves(ctx context.Context) ([]DossierMove, error) {
	rows, err := db.R().QueryContext(ctx, `SELECT seq, from_path, to_path FROM dossier_moves ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DossierMove
	for rows.Next() {
		var m DossierMove
		if err := rows.Scan(&m.Seq, &m.From, &m.To); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// DossierMoved forgets the dossier move seq, done on disk.
func (db *DB) DossierMoved(ctx context.Context, seq int64) error {
	_, err := db.Write(ctx, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM dossier_moves WHERE seq = ?`, seq)
		return err
	})
	return err
}
