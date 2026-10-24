package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/wloophq/wloop/internal/domain/workflow"
)

// WorkflowActivity is what one workflow did, as doctor reports it.
type WorkflowActivity struct {
	LastRun  time.Time // the newest done run; zero when none is stored
	Refusals int       // the step refusals since the time asked
}

// WorkflowActivity is each workflow's newest done run and its step
// refusals since since, by workflow name.
func (db *DB) WorkflowActivity(ctx context.Context, since time.Time) (map[string]WorkflowActivity, error) {
	out := map[string]WorkflowActivity{}
	rows, err := db.R().QueryContext(ctx, `SELECT workflow, max(at) FROM workflow_runs WHERE status = ? GROUP BY workflow`, string(workflow.RunDone))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var wf, at string
		if err := rows.Scan(&wf, &at); err != nil {
			return nil, err
		}
		a := out[wf]
		if a.LastRun, err = parseStoredTime(at); err != nil {
			return nil, fmt.Errorf("the last run of %s: %w", wf, err)
		}
		out[wf] = a
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	refused, err := db.R().QueryContext(ctx, `SELECT substr(key, 1, instr(key, '/') - 1) AS wf, count(*) FROM events
		WHERE kind = ? AND at >= ? GROUP BY wf`, workflow.KindStepRefused, eventTime(since))
	if err != nil {
		return nil, err
	}
	defer refused.Close()
	for refused.Next() {
		var wf string
		var n int
		if err := refused.Scan(&wf, &n); err != nil {
			return nil, err
		}
		a := out[wf]
		a.Refusals = n
		out[wf] = a
	}
	return out, refused.Err()
}
