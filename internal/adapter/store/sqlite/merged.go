package sqlite

import (
	"context"
	"database/sql"
	"slices"

	"github.com/wloophq/wloop/internal/domain/merge"
	"github.com/wloophq/wloop/internal/domain/task"
)

// maxMergeHops bounds the alias walk, which a hand-edited cycle of aliases would otherwise never end.
const maxMergeHops = 64

// mergedIntoSQL walks the aliases back from a task; a task's merge order is the id of its newest merge
// record, and an alias with none, as a v7 migration wrote, comes first.
const mergedIntoSQL = `WITH RECURSIVE m(id, hops) AS (
		SELECT old_id, 1 FROM task_aliases WHERE new_id = ?1 AND old_id NOT IN (SELECT id FROM tasks)
		UNION SELECT a.old_id, m.hops + 1 FROM task_aliases a JOIN m ON a.new_id = m.id
			WHERE m.hops < ?2 AND a.old_id NOT IN (SELECT id FROM tasks))
	SELECT m.id FROM m LEFT JOIN (SELECT unit_key, max(id) AS seq FROM events WHERE provider = 'task' AND kind = ?3 GROUP BY unit_key) r
		ON r.unit_key = m.id
	WHERE m.id != ?1
	GROUP BY m.id ORDER BY max(r.seq), m.id`

// MergedInto are the tasks merged into task id, transitively, in merge order: the old ids of the aliases
// that lead to id, hop by hop, that no stored task holds.
func (db *DB) MergedInto(ctx context.Context, id task.ID) ([]task.ID, error) {
	var out []task.ID
	err := eachRow(ctx, db.R(), mergedIntoSQL, []any{string(id), maxMergeHops, merge.RecordKind}, func(rows *sql.Rows) error {
		var m string
		if err := rows.Scan(&m); err != nil {
			return err
		}
		if !slices.Contains(out, task.ID(m)) {
			out = append(out, task.ID(m))
		}
		return nil
	})
	return out, err
}
