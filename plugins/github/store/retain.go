package store

import (
	"context"
	"time"

	"github.com/wloophq/wloop/plugins/github/pr"
)

// Retain drops what the host's retention ends: every row of a pull request
// merged or closed before endedBefore, and the fetches before
// fetchesBefore, in one transaction. It returns the pull requests it
// dropped, which no later report names.
func (db *DB) Retain(ctx context.Context, endedBefore, fetchesBefore time.Time) ([]pr.Ref, error) {
	var dropped []pr.Ref
	_, err := db.Write(ctx, func(tx *Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT owner, repo, number FROM pull_requests
			WHERE state != ? AND max(merged_at, closed_at) != '' AND max(merged_at, closed_at) < ? ORDER BY owner, repo, number`,
			pr.StateOpen, formatTime(endedBefore))
		if err != nil {
			return err
		}
		for rows.Next() {
			var ref pr.Ref
			if err := rows.Scan(&ref.Owner, &ref.Repo, &ref.Number); err != nil {
				rows.Close()
				return err
			}
			dropped = append(dropped, ref)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, ref := range dropped {
			for _, q := range []string{`DELETE FROM pr_seen WHERE owner = ? AND repo = ? AND number = ?`,
				`DELETE FROM pr_details WHERE owner = ? AND repo = ? AND number = ?`,
				`DELETE FROM pull_requests WHERE owner = ? AND repo = ? AND number = ?`} {
				if _, err := tx.ExecContext(ctx, q, ref.Owner, ref.Repo, ref.Number); err != nil {
					return err
				}
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM overrides WHERE unit_key = ?`, ref.String()); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM fetches WHERE at < ?`, formatTime(fetchesBefore))
		return err
	})
	return dropped, err
}
