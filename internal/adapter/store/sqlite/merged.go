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

// mergeAlias holds on an alias a whose old task's newest merge record names a's new task: an alias the v7
// migration wrote carries no history, since the migration moved its rows.
const mergeAlias = `json_extract((SELECT e.data FROM events e WHERE e.id = (SELECT max(rec.id) FROM events rec
	WHERE rec.provider = 'task' AND rec.unit_key = a.old_id AND rec.kind = '` + merge.RecordKind + `')), '$.` + merge.IntoField + `') = a.new_id`

// mergedIntoSQL walks the merge aliases back from a task, in the order of the tasks' newest merge records.
const mergedIntoSQL = `WITH RECURSIVE m(id, hops) AS (
		SELECT a.old_id, 1 FROM task_aliases a WHERE a.new_id = ?1 AND a.old_id NOT IN (SELECT id FROM tasks) AND ` + mergeAlias + `
		UNION SELECT a.old_id, m.hops + 1 FROM task_aliases a JOIN m ON a.new_id = m.id
			WHERE m.hops < ?2 AND a.old_id NOT IN (SELECT id FROM tasks) AND ` + mergeAlias + `)
	SELECT x.id FROM (SELECT DISTINCT id FROM m WHERE id != ?1) x
	ORDER BY (SELECT max(e.id) FROM events e WHERE e.provider = 'task' AND e.unit_key = x.id AND e.kind = ?3), x.id`

// MergedInto are the tasks merged into task id, transitively, in merge order: the old ids of the aliases
// that lead to id, hop by hop, that no stored task holds and whose newest merge record names the alias's
// new id.
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
