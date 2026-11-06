package sqlite

import "context"

// UnhandledMergeColumns are the live schema's columns that can name a task and that no merge handling covers.
func UnhandledMergeColumns(db *DB) ([]string, error) {
	return unhandledMergeColumns(context.Background(), db.R())
}

// TaskColumns are the columns taskRefs knows by name, as table.column.
func TaskColumns() []string {
	out := make([]string, len(taskColumns))
	for i, c := range taskColumns {
		out[i] = c.table + "." + c.column
	}
	return out
}
