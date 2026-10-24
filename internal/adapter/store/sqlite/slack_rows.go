package sqlite

import (
	"context"
	"time"
)

const slackRowObjectsSQL = `SELECT ` + slackObjectColumns + ` FROM slack_objects
	WHERE watched = 1 OR last_activity_at >= ? ORDER BY object`

// RowObjects are the objects watched or active at or after since.
func (s *SlackStore) RowObjects(ctx context.Context, since time.Time) ([]ObjectRow, error) {
	rows, err := s.db.R().QueryContext(ctx, slackRowObjectsSQL, slackTime(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ObjectRow
	for rows.Next() {
		o, err := scanObject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
