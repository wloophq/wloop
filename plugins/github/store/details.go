package store

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/wloophq/wloop/plugins/github/pr"
)

const detailChunk = 500

// DetailVersion is the shape of Detail this release reads; a stored detail
// of another version is read again by the next fast run.
const DetailVersion = 2

// Detail is what deciding on a pull request reads beyond its row: the
// collector's full read, stored as GitHub returned it.
type Detail struct {
	Version                 int       `json:"-"`
	ReadAt                  time.Time `json:"-"`
	AuthorAssociation       string
	Visibility              string // the repository's: public, private or internal
	Labels                  []string
	Commits                 []pr.Commit // the newest 50, oldest first
	Reviews                 []pr.Review // the newest 100, oldest first; HasContent set, ByViewer unset
	LatestReviews           []pr.Review // latestReviews(first: 20)
	NewestChangesRequestOid string
	Comments                []pr.Comment // the newest 100, oldest first
	Checks                  []pr.Check   // the head's contexts, first 50
	ThreadsTotal            int
	// OpenThreads holds the unresolved threads among the newest 50 when
	// ThreadsRead: read for a pull request with ThreadsTotal > 0 the viewer
	// authored or develops in a workspace, and known empty when ThreadsTotal
	// is 0.
	OpenThreads []pr.Thread
	ThreadsRead bool
}

// Fresh tells a detail of this release's version read within maxAge of now.
func (d Detail) Fresh(now time.Time, maxAge time.Duration) bool {
	return d.Version == DetailVersion && now.Sub(d.ReadAt) < maxAge
}

// saveDetail writes d over was unless they are equal; a new body changes
// ref's facts, a new version or read time alone does not. It returns d as
// stored.
func saveDetail(ctx context.Context, tx *Tx, ref pr.Ref, d Detail, was *storedDetail, seenAt time.Time) (storedDetail, error) {
	if d.ReadAt.IsZero() {
		d.ReadAt = seenAt
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return storedDetail{}, err
	}
	now := storedDetail{version: d.Version, readAt: formatTime(d.ReadAt), body: string(raw)}
	switch {
	case was != nil && *was == now:
	case was != nil && was.body == now.body:
		_, err = tx.ExecContext(ctx, `UPDATE pr_details SET version = ?, read_at = ? WHERE owner = ? AND repo = ? AND number = ?`,
			now.version, now.readAt, ref.Owner, ref.Repo, ref.Number)
	default:
		_, err = tx.ExecContext(ctx, `INSERT INTO pr_details (owner, repo, number, version, read_at, body) VALUES (?,?,?,?,?,?)
			ON CONFLICT(owner, repo, number) DO UPDATE SET version = excluded.version, read_at = excluded.read_at, body = excluded.body`,
			ref.Owner, ref.Repo, ref.Number, now.version, now.readAt, now.body)
		tx.Touch(ref)
	}
	return now, err
}

// SaveDetail stores d as ref's detail unless it equals the stored one.
func (db *DB) SaveDetail(ctx context.Context, ref pr.Ref, d Detail) error {
	_, err := db.Write(ctx, func(tx *Tx) error {
		prior, err := storedRows(ctx, tx, []pr.Ref{ref})
		if err != nil {
			return err
		}
		_, err = saveDetail(ctx, tx, ref, d, prior[ref].detail, d.ReadAt)
		return err
	})
	return err
}

// Details returns the stored detail of each ref that has one, one query per
// 500 refs.
func (db *DB) Details(ctx context.Context, refs []pr.Ref) (map[pr.Ref]Detail, error) {
	return db.details(ctx, refs, true)
}

// DetailStamps are the version and read time of each ref's stored detail,
// without its body.
func (db *DB) DetailStamps(ctx context.Context, refs []pr.Ref) (map[pr.Ref]Detail, error) {
	return db.details(ctx, refs, false)
}

func (db *DB) details(ctx context.Context, refs []pr.Ref, withBody bool) (map[pr.Ref]Detail, error) {
	body := "body"
	if !withBody {
		body = "''"
	}
	out := make(map[pr.Ref]Detail, len(refs))
	for chunk := range slices.Chunk(refs, detailChunk) {
		marks, args := refValues(chunk)
		rows, err := db.db.QueryContext(ctx, refsIn(`SELECT owner, repo, number, version, read_at, `+body+` FROM pr_details WHERE (owner, repo, number)`, marks), args...)
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
				if body != "" {
					if err := json.Unmarshal([]byte(body), &d); err != nil {
						return err
					}
				}
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
