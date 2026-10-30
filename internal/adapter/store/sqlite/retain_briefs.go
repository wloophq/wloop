package sqlite

import (
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/timeline"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// briefsInUse are the runs a unit's brief, its last attempt or a turn reads.
const briefsInUse = `(SELECT run FROM unit_briefs UNION SELECT last_run FROM unit_briefs UNION SELECT brief_run FROM turns)`

// retainBriefBodiesSQL empties the brief and the file list of runs no one
// reads, keeping the changed-file count a timing reads; the json_type test
// keeps a second pass from matching the rows the first emptied.
const retainBriefBodiesSQL = `UPDATE brief_runs SET brief = '', input = json_remove(input, '$.files')
		WHERE started_at < ? AND (brief != '' OR json_type(input, '$.files') IS NOT NULL)
		AND id NOT IN ` + briefsInUse

// cursorRuns are the runs the extraction writes, one per unit per day, and
// the legacy runs v4 copied from its cursors: their briefs go with their
// cursor. They are picked by trigger so that no cursor row, whatever its
// provider, reaches a brief another builder wrote.
const cursorRuns = `(SELECT id FROM brief_runs WHERE trigger IN ('extraction', 'legacy'))`

// untaskedBrief keeps the brief rows of table on a unit linked to a task.
func untaskedBrief(table string) string {
	return ` AND NOT EXISTS (SELECT 1 FROM task_units tu WHERE tu.provider = ` + table + `.provider AND tu.unit_key = ` + table + `.unit_key)`
}

var (
	// retainCursorRunsSQL deletes the daily runs no one reads past the
	// cutoff, and every run but a turn's of a unit whose cursor is gone; it
	// runs after the orphan unit_briefs rows are deleted.
	retainCursorRunsSQL = `DELETE FROM brief_runs WHERE trigger IN ('extraction', 'legacy') AND id NOT IN (SELECT brief_run FROM turns)
		AND (started_at < ? AND id NOT IN ` + briefsInUse + ` OR (provider, unit_key) NOT IN ` + cursorUnits + `)` + untaskedBrief("brief_runs")
	orphanCursorBriefsSQL = `DELETE FROM unit_briefs WHERE run IN ` + cursorRuns + `
		AND (provider, unit_key) NOT IN ` + cursorUnits + untaskedBrief("unit_briefs")
	orphanCursorRecordsSQL = `DELETE FROM events WHERE kind = '` + timeline.KindRecord + `' AND key = '` + builtin.BuildsBrief + `'
		AND provider != 'task' AND (provider, unit_key) NOT IN ` + cursorUnits + `
		AND NOT EXISTS (SELECT 1 FROM unit_briefs b WHERE b.provider = events.provider AND b.unit_key = events.unit_key)` + untaskedBrief("events")
)

// briefRecordsKey is the Retained key of the brief records a unit whose
// cursor is gone kept.
const briefRecordsKey = "brief_records"

// briefs retains brief rows as of before: the bodies of runs no one reads,
// and, once the Slack plugin reported, the extraction's past daily runs no
// one reads and the brief rows of a unit not among cursors, the bound list
// of the units it reports.
func (r *retainer) briefs(before, cursors string, reported bool) {
	r.exec("brief_runs", port.TopicBriefs, retainBriefBodiesSQL, before)
	if !reported {
		return
	}
	r.exec("unit_briefs", port.TopicBriefs, orphanCursorBriefsSQL, cursors)
	r.exec(briefRecordsKey, port.TopicBriefs|port.TopicLoopEvents, orphanCursorRecordsSQL, cursors)
	r.exec("brief_runs", port.TopicBriefs, retainCursorRunsSQL, before, cursors)
}

// briefEndedTables are the brief rows of an ended pull request, in delete
// order among endedTables.
var briefEndedTables = []endedTable{
	{"unit_briefs", port.TopicBriefs, `DELETE FROM unit_briefs WHERE provider = 'github' AND unit_key IN ` + untaskedUnits},
	{"brief_runs", port.TopicBriefs, `DELETE FROM brief_runs WHERE provider = 'github' AND unit_key IN ` + untaskedUnits},
}
