package store

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/wloophq/wloop/plugins/github/pr"
)

const detailChunk = 500

// Detail is a pull request's full read beyond its row: Body is the read as
// JSON, in the shape of Version.
type Detail struct {
	Version int
	ReadAt  time.Time
	Body    json.RawMessage
}

// SaveDetail stores d as ref's detail unless it equals the stored one. A
// new body changes ref's facts; a new version or read time alone does not.
func (db *DB) SaveDetail(ctx context.Context, ref pr.Ref, d Detail) error {
	readAt := formatTime(d.ReadAt)
	_, err := db.Write(ctx, func(tx *Tx) error {
		var version int
		var wasAt, body string
		err := tx.QueryRowContext(ctx, `SELECT version, read_at, body FROM pr_details WHERE owner = ? AND repo = ? AND number = ?`,
			ref.Owner, ref.Repo, ref.Number).Scan(&version, &wasAt, &body)
		switch {
		case err != nil && !noRows(err):
			return err
		case err == nil && body == string(d.Body):
			if version == d.Version && wasAt == readAt {
				return nil
			}
			_, err = tx.ExecContext(ctx, `UPDATE pr_details SET version = ?, read_at = ? WHERE owner = ? AND repo = ? AND number = ?`,
				d.Version, readAt, ref.Owner, ref.Repo, ref.Number)
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO pr_details (owner, repo, number, version, read_at, body) VALUES (?,?,?,?,?,?)
			ON CONFLICT(owner, repo, number) DO UPDATE SET version = excluded.version, read_at = excluded.read_at, body = excluded.body`,
			ref.Owner, ref.Repo, ref.Number, d.Version, readAt, string(d.Body))
		tx.Touch(ref)
		return err
	})
	return err
}

// Details returns the stored detail of each ref that has one, one query per
// 500 refs.
func (db *DB) Details(ctx context.Context, refs []pr.Ref) (map[pr.Ref]Detail, error) {
	out := make(map[pr.Ref]Detail, len(refs))
	for chunk := range slices.Chunk(refs, detailChunk) {
		marks, args := refValues(chunk)
		rows, err := db.db.QueryContext(ctx, refsIn(`SELECT owner, repo, number, version, read_at, body FROM pr_details WHERE (owner, repo, number)`, marks), args...)
		if err != nil {
			return nil, err
		}
		err = func() error {
			defer rows.Close()
			for rows.Next() {
				var ref pr.Ref
				var d Detail
				var readAt, body string
				if err := rows.Scan(&ref.Owner, &ref.Repo, &ref.Number, &d.Version, &readAt, &body); err != nil {
					return err
				}
				if d.ReadAt, err = parseTime(readAt); err != nil {
					return err
				}
				d.Body = json.RawMessage(body)
				out[ref] = d
			}
			return rows.Err()
		}()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
