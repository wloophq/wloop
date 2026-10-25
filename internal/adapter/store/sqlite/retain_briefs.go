package sqlite

import "github.com/wloophq/wloop/internal/core/port"

const retainBriefBodiesSQL = `UPDATE brief_runs SET brief = '', input = '{}'
		WHERE started_at < ? AND (brief != '' OR input != '{}')
		AND id NOT IN (SELECT run FROM unit_briefs UNION SELECT last_run FROM unit_briefs UNION SELECT brief_run FROM turns)`

// briefEndedTables are the brief rows of an ended pull request, in delete
// order among endedTables.
var briefEndedTables = []endedTable{
	{"unit_briefs", port.TopicBriefs, `DELETE FROM unit_briefs WHERE provider = 'github' AND unit_key IN ` + endedUnits, false},
	{"brief_runs", port.TopicBriefs, `DELETE FROM brief_runs WHERE provider = 'github' AND unit_key IN ` + endedUnits, false},
	{"unit_tags", port.TopicBriefs, `DELETE FROM unit_tags WHERE provider = 'github' AND unit_key IN ` + endedUnits, false},
}
