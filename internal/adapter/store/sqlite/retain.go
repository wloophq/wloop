package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/views"
	"github.com/wloophq/wloop/internal/domain/event"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// Retention says how long each kind of row is kept. Zero keeps forever.
// Every event, turn, record, answer, plan item and brief of a task, or of
// a unit linked to a task, is kept forever: no rule reaches them.
type Retention struct {
	SessionEvents time.Duration // events of no unit or a session unit, of the kinds in SessionEventKinds
	ViewEvents    time.Duration // view_applied, view_override and view_sink_failed events
	// ClosedSessions drops the sessions closed that long ago that no open
	// turn names, and the agent sessions last seen that long ago whose turns
	// all have their usage read.
	// It also drops the workflow runs of session units that old whose
	// session row is gone.
	ClosedSessions time.Duration
	Stops          time.Duration // turn_stops
	Instructions   time.Duration // turn_instructions, then the parts no row lists
	// BriefBodies empties brief and input of the runs started that long ago
	// that no unit_briefs row or turn references.
	BriefBodies time.Duration
	ViewRows    time.Duration // view_applied rows of pull requests merged or closed that long ago
	EndedPRs    time.Duration // a pull request merged or closed that long ago, with every row of it
	// WorkflowRuns drops the done workflow runs that old. A dismissed run
	// keeps its key terminal, so it goes only with its unit's rows.
	WorkflowRuns   time.Duration
	PlanItems      time.Duration // plan items not pending, last saved that long ago
	WorkflowEvents time.Duration // the engine's events, workflow.EngineKinds
	// UnitlessLoopRows drops the turns, records, answers, intakes, events,
	// workflow runs and plan items of a github unit the GitHub plugin does
	// not report, with no overrides row and nothing newer.
	UnitlessLoopRows time.Duration
	Classifications  time.Duration // classifications and classify_days
	Fetches          time.Duration
	// SubjectMarks drops the marks of tasks done, in the trash or gone,
	// and of Slack units the Slack plugin does not report, with no item and
	// no task.
	SubjectMarks bool
	VacuumPages  int // PRAGMA incremental_vacuum(VacuumPages) after each pass; 0 runs none
}

// SessionEventKinds are the bookkeeping kinds SessionEvents drops. Pause
// events and every event of a pull request are kept.
var SessionEventKinds = []string{
	turns.KindSessionIdle, turns.KindSessionActive, turns.KindSessionStarted, turns.KindSessionLaunched,
	turns.KindSessionAbandoned, turns.KindSessionClosed, turns.KindMCPSeen, turns.KindReconcileReport,
	turns.KindSessionTyped, turns.KindSessionPrompted, turns.KindSessionUnit,
}

const retentionWindow = 30 * 24 * time.Hour

// DefaultRetention is what the daemon's retain job applies. ViewRows stays
// zero: the views re-plan a unit whose applied rows are gone, and every
// ended unit with a turn book is still planned.
var DefaultRetention = Retention{
	SessionEvents:    retentionWindow,
	ViewEvents:       retentionWindow,
	ClosedSessions:   retentionWindow,
	Stops:            turns.StopsKept,
	Instructions:     retentionWindow,
	BriefBodies:      7 * 24 * time.Hour,
	EndedPRs:         400 * 24 * time.Hour,
	WorkflowRuns:     retentionWindow,
	PlanItems:        7 * 24 * time.Hour,
	WorkflowEvents:   retentionWindow,
	UnitlessLoopRows: retentionWindow,
	Classifications:  retentionWindow,
	Fetches:          retentionWindow,
	SubjectMarks:     true,
	VacuumPages:      4096,
}

// Retained counts the rows a pass deleted or compacted, per table; the
// events SessionEvents and ViewEvents drop count under their own keys, and
// "events" counts those of ended pull requests.
type Retained map[string]int

// The Retained keys of the events SessionEvents and ViewEvents drop.
const (
	SessionEventsKey = "session_events"
	ViewEventsKey    = "view_events"
)

// The Retained keys of WorkflowEvents and UnitlessLoopRows; WorkflowRuns
// and PlanItems count under their tables.
const (
	WorkflowEventsKey   = "workflow_events"
	UnitlessLoopRowsKey = "unitless_loop_rows"
)

// String lists the tables a pass changed, in name order: "events 3,
// sessions 1", or "nothing".
func (r Retained) String() string {
	if len(r) == 0 {
		return "nothing"
	}
	parts := make([]string, 0, len(r))
	for _, table := range slices.Sorted(maps.Keys(r)) {
		parts = append(parts, fmt.Sprintf("%s %d", table, r[table]))
	}
	return strings.Join(parts, ", ")
}

// endedReportsSQL selects the unit key of every pull request the GitHub
// plugin last reported merged or closed before its parameter.
var endedReportsSQL = `SELECT unit_key FROM unit_snapshots WHERE provider = 'github' AND json_extract(snapshot, '$.data.pr.State') IN ('MERGED', 'CLOSED')
		AND max(` + reportedDay("MergedAt") + `, ` + reportedDay("ClosedAt") + `) BETWEEN 1721426 AND julianday(?)`

// reportedDay is the Julian day of the time field of a reported pull
// request, 0 for none.
func reportedDay(field string) string {
	return `coalesce(julianday(json_extract(snapshot, '$.data.pr.` + field + `')), 0)`
}

const (
	retainViewEventsSQL = `DELETE FROM events WHERE kind IN (?, ?, ?) AND at < ?` + untaskedEvents
	retainSessionsSQL   = `DELETE FROM sessions WHERE closed_at != '' AND closed_at < ?
		AND session_id NOT IN (SELECT session FROM turns WHERE state IN ('sent', 'started', 'asked')
			UNION SELECT sent_to FROM turns WHERE state IN ('sent', 'started', 'asked'))`
	retainSessionHandlesSQL = `DELETE FROM session_handles WHERE session_id NOT IN (SELECT session_id FROM sessions)`
	retainAgentSessionsSQL  = `DELETE FROM agent_sessions WHERE last_seen_at != '' AND last_seen_at < ?
		AND agent_session NOT IN (SELECT agent_session FROM turns WHERE usage_source = '')`
	retainStopsSQL        = `DELETE FROM turn_stops WHERE at < ?`
	retainInstructionsSQL = `DELETE FROM turn_instructions WHERE created_at < ?`
	retainPartsSQL        = `DELETE FROM instruction_parts WHERE sha NOT IN
		(SELECT j.value FROM turn_instructions r, json_each(r.parts) j WHERE r.parts != '')`
	retainViewRowsSQL     = `DELETE FROM view_applied WHERE provider = 'github' AND unit_key IN ` + endedUnits
	retainPlanItemsSQL    = `DELETE FROM plan_items WHERE decision NOT IN ('pending', 'deferred') AND saved_at < ?` + untaskedPlanItems
	retainWorkflowRunsSQL = `DELETE FROM workflow_runs WHERE status = 'done' AND at < ?`
	retainSessionRunsSQL  = `DELETE FROM workflow_runs WHERE provider = 'session' AND at < ? AND unit_key NOT IN (SELECT session_id FROM sessions)`
	retainSessionMarksSQL = `DELETE FROM unit_marks WHERE provider = 'session' AND unit_key NOT IN (SELECT session_id FROM sessions)`
	retainTaskMarksSQL    = `DELETE FROM unit_marks WHERE provider = 'task' AND unit_key NOT IN (SELECT id FROM tasks WHERE category NOT IN (?, ?))`
	retainSlackMarksSQL   = `DELETE FROM unit_marks WHERE provider = 'slack'
			AND (provider, unit_key) NOT IN ` + cursorUnits + `
			AND NOT EXISTS (SELECT 1 FROM items i WHERE i.provider = 'slack' AND i.unit_key = unit_marks.unit_key)
			AND NOT EXISTS (SELECT 1 FROM task_units u WHERE u.provider = 'slack' AND u.unit_key = unit_marks.unit_key)`
	retainFetchesSQL         = `DELETE FROM fetches WHERE at < ?`
	retainClassificationsSQL = `DELETE FROM classifications WHERE at < ?`
	retainClassifyDaysSQL    = `DELETE FROM classify_days WHERE day < ?`
	unitlessUnitsSQL         = `SELECT unit_key FROM (
		SELECT unit_key, max(sent_at, started_at, ended_at) AS t FROM turns WHERE provider = 'github'
		UNION ALL SELECT unit_key, at FROM turn_records WHERE provider = 'github'
		UNION ALL SELECT unit_key, at FROM turn_answers WHERE provider = 'github'
		UNION ALL SELECT unit_key, max(accepted_at, briefing_at, starting_at, ended_at) FROM intakes WHERE provider = 'github'
		UNION ALL SELECT unit_key, at FROM events WHERE provider = 'github'
		UNION ALL SELECT unit_key, at FROM workflow_runs WHERE provider = 'github'
		UNION ALL SELECT unit_key, saved_at FROM plan_items WHERE provider = 'github'
	) WHERE unit_key NOT IN (SELECT value FROM json_each(?))
		AND unit_key NOT IN (SELECT unit_key FROM overrides WHERE provider = 'github')
	GROUP BY unit_key HAVING max(t) != '' AND max(t) < ?`
)

// A step that ran on a unit of work lives as long as that unit's events.
var retainWorkflowEventsSQL = `DELETE FROM events WHERE kind IN (` + strings.TrimSuffix(strings.Repeat("?, ", len(workflow.EngineKinds)), ", ") + `) AND at < ?
	AND NOT (kind = '` + workflow.KindStepRan + `' AND provider NOT IN ('', '` + string(unit.Session) + `', '` + string(unit.Workspace) + `'))` + untaskedEvents

// unitlessTables are the endedTables UnitlessLoopRows deletes from; like
// every endedTables query, they skip a unit linked to a task.
var unitlessTables = []string{"turn_usage", "turns", "turn_records", "turn_answers", "intakes", "events", "workflow_runs", "plan_items", "unit_marks", "round_state"}

// Session-keyed bookkeeping is stored under its session unit, the rest
// under no unit; a pull request's events never match.
var retainSessionEventsSQL = `DELETE FROM events WHERE kind IN (` + strings.TrimSuffix(strings.Repeat("?, ", len(SessionEventKinds)), ", ") +
	`) AND at < ? AND +provider IN ('', 'session')` + untaskedEvents

// untaskedEvents and untaskedPlanItems keep every row on a task's unit,
// or on a unit linked to a task, out of a retention rule.
const (
	untaskedEvents = ` AND events.provider != 'task'
		AND NOT EXISTS (SELECT 1 FROM task_units tu WHERE tu.provider = events.provider AND tu.unit_key = events.unit_key)`
	untaskedPlanItems = ` AND plan_items.provider != 'task'
		AND NOT EXISTS (SELECT 1 FROM task_units tu WHERE tu.provider = plan_items.provider AND tu.unit_key = plan_items.unit_key)`
)

// untaskedUnits reads the bound JSON list of unit keys as endedUnits does,
// but those a task holds, whose rows are kept forever.
const untaskedUnits = `(SELECT value FROM json_each(?) WHERE value NOT IN (SELECT unit_key FROM task_units))`

// endedUnits and endedRefs read the bound JSON list of the ended pull
// requests' unit keys, or of their [owner, repo, number] triples;
// cursorUnits reads that of the keys of the Slack units the plugin reports
// as [provider, unit_key] pairs.
const (
	endedUnits  = `(SELECT value FROM json_each(?))`
	endedRefs   = `(SELECT json_extract(value, '$[0]'), json_extract(value, '$[1]'), json_extract(value, '$[2]') FROM json_each(?))`
	cursorUnits = `(SELECT 'slack', value FROM json_each(?))`
)

type endedTable struct {
	table  string
	topics port.Topic
	query  string
}

// endedTables are the core's rows of an ended pull request no task links,
// in delete order: turn_usage goes before the turns it hangs from.
var endedTables = slices.Concat([]endedTable{
	{"turn_usage", port.TopicUsage, `DELETE FROM turn_usage WHERE turn_seq IN (SELECT seq FROM turns WHERE provider = 'github' AND unit_key IN ` + untaskedUnits + `)`},
	{"turns", port.TopicTurns, `DELETE FROM turns WHERE provider = 'github' AND unit_key IN ` + untaskedUnits},
	{"turn_records", port.TopicTurns, `DELETE FROM turn_records WHERE provider = 'github' AND unit_key IN ` + untaskedUnits},
	{"turn_answers", port.TopicTurns, `DELETE FROM turn_answers WHERE provider = 'github' AND unit_key IN ` + untaskedUnits},
	{"intakes", port.TopicIntakes, `DELETE FROM intakes WHERE provider = 'github' AND unit_key IN ` + untaskedUnits},
	{"events", port.TopicSourceEvents | port.TopicLoopEvents, `DELETE FROM events WHERE provider = 'github' AND unit_key IN ` + untaskedUnits},
	{"overrides", port.TopicOverrides, `DELETE FROM overrides WHERE provider = 'github' AND unit_key IN ` + untaskedUnits},
	{"workflow_runs", 0, `DELETE FROM workflow_runs WHERE provider = 'github' AND unit_key IN ` + untaskedUnits},
	{"plan_items", port.TopicPending, `DELETE FROM plan_items WHERE provider = 'github' AND unit_key IN ` + untaskedUnits},
	{"unit_marks", port.TopicRounds, `DELETE FROM unit_marks WHERE provider = 'github' AND unit_key IN ` + untaskedUnits},
	{"round_state", port.TopicRounds, `DELETE FROM round_state WHERE provider = 'github' AND unit_key IN ` + untaskedUnits},
	{"view_applied", port.TopicViews, `DELETE FROM view_applied WHERE provider = 'github' AND unit_key IN ` + untaskedUnits},
}, briefEndedTables)

// Retain applies p as of now in one write transaction, noting Change.All
// with the topics of the tables it changed, then runs incremental_vacuum and
// PRAGMA optimize outside it. EndedPRs runs first, so the rules after it see
// the sessions and runs only an ended pull request's turns held, and a
// second pass at the same now changes nothing and notes nothing.
func (db *DB) Retain(ctx context.Context, now time.Time, p Retention) (Retained, error) {
	plugins := reportedUnits{db}
	var in retainInputs
	var err error
	if p.EndedPRs > 0 {
		if in.ended, err = plugins.endedPRs(ctx, cutoff(now, p.EndedPRs)); err != nil {
			return nil, err
		}
	}
	if p.ViewRows > 0 {
		if in.viewEnded, err = plugins.endedPRs(ctx, cutoff(now, p.ViewRows)); err != nil {
			return nil, err
		}
	}
	if p.UnitlessLoopRows > 0 {
		if in.prKeys, in.githubReported, err = plugins.live(ctx, reportedGitHubSQL); err != nil {
			return nil, err
		}
	}
	if p.BriefBodies > 0 || p.SubjectMarks {
		if in.slackUnits, in.slackReported, err = plugins.live(ctx, reportedSlackSQL); err != nil {
			return nil, err
		}
	}
	var out Retained
	_, err = db.Write(ctx, func(tx *Tx) error {
		out = Retained{}
		r := &retainer{ctx: ctx, tx: tx, out: out}
		if p.EndedPRs > 0 {
			r.endedPRs(in.ended)
		}
		if p.SessionEvents > 0 {
			args := make([]any, 0, len(SessionEventKinds)+1)
			for _, k := range SessionEventKinds {
				args = append(args, k)
			}
			r.exec(SessionEventsKey, port.TopicLoopEvents, retainSessionEventsSQL, append(args, cutoff(now, p.SessionEvents))...)
		}
		if p.ViewEvents > 0 {
			r.exec(ViewEventsKey, port.TopicLoopEvents, retainViewEventsSQL,
				views.KindViewApplied, views.KindViewOverride, views.KindViewSinkFailed, cutoff(now, p.ViewEvents))
		}
		if p.ClosedSessions > 0 {
			r.exec("sessions", port.TopicSessions, retainSessionsSQL, cutoff(now, p.ClosedSessions))
			r.exec("session_handles", 0, retainSessionHandlesSQL)
			r.exec("agent_sessions", port.TopicSessions, retainAgentSessionsSQL, cutoff(now, p.ClosedSessions))
			r.exec("workflow_runs", 0, retainSessionRunsSQL, cutoff(now, p.ClosedSessions))
			r.exec("unit_marks", port.TopicMarks, retainSessionMarksSQL)
		}
		if p.Stops > 0 {
			r.exec("turn_stops", port.TopicSessions, retainStopsSQL, cutoff(now, p.Stops))
		}
		if p.Instructions > 0 {
			r.exec("turn_instructions", port.TopicRounds, retainInstructionsSQL, cutoff(now, p.Instructions))
			r.exec("instruction_parts", port.TopicRounds, retainPartsSQL)
		}
		if p.BriefBodies > 0 {
			r.briefs(cutoff(now, p.BriefBodies), in.slackPairs(), in.slackReported)
		}
		if p.ViewRows > 0 {
			r.exec("view_applied", port.TopicViews, retainViewRowsSQL, keyList(in.viewEnded))
		}
		if p.PlanItems > 0 {
			r.exec("plan_items", 0, retainPlanItemsSQL, cutoff(now, p.PlanItems))
		}
		if p.WorkflowRuns > 0 {
			r.exec("workflow_runs", 0, retainWorkflowRunsSQL, cutoff(now, p.WorkflowRuns))
		}
		if p.WorkflowEvents > 0 {
			args := make([]any, 0, len(workflow.EngineKinds)+1)
			for _, k := range workflow.EngineKinds {
				args = append(args, k)
			}
			r.exec(WorkflowEventsKey, port.TopicLoopEvents, retainWorkflowEventsSQL, append(args, cutoff(now, p.WorkflowEvents))...)
		}
		if p.UnitlessLoopRows > 0 && in.githubReported {
			r.unitless(cutoff(now, p.UnitlessLoopRows), jsonList(in.prKeys))
		}
		if p.Classifications > 0 {
			r.exec("classifications", 0, retainClassificationsSQL, cutoff(now, p.Classifications))
			r.exec("classify_days", 0, retainClassifyDaysSQL, now.Add(-p.Classifications).UTC().Format(time.DateOnly))
		}
		if p.SubjectMarks {
			r.exec("unit_marks", port.TopicMarks, retainTaskMarksSQL, openArgs()...)
			if in.slackReported {
				r.exec("unit_marks", port.TopicMarks, retainSlackMarksSQL, in.slackPairs())
			}
		}
		if p.Fetches > 0 {
			r.exec("fetches", 0, retainFetchesSQL, cutoff(now, p.Fetches))
		}
		return r.err
	})
	if err != nil {
		return nil, err
	}
	stmts := []string{`PRAGMA optimize`}
	if p.VacuumPages > 0 {
		stmts = append([]string{fmt.Sprintf(`PRAGMA incremental_vacuum(%d)`, p.VacuumPages)}, stmts...)
	}
	if err := db.runOnWriter(ctx, stmts...); err != nil {
		return out, err
	}
	return out, nil
}

// cutoff is now less d in event.TimeLayout. Against it a stored time in
// that layout, in RFC 3339 or with trimmed nanoseconds compares as text
// without ever reading as earlier than it is.
func cutoff(now time.Time, d time.Duration) string {
	return now.Add(-d).UTC().Format(event.TimeLayout)
}

type retainer struct {
	ctx context.Context
	tx  *Tx
	out Retained
	err error
}

func (r *retainer) exec(table string, topics port.Topic, query string, args ...any) {
	if r.err != nil {
		return
	}
	res, err := r.tx.ExecContext(r.ctx, query, args...)
	if err != nil {
		r.err = fmt.Errorf("retain %s: %w", table, err)
		return
	}
	n, err := res.RowsAffected()
	if err != nil {
		r.err = err
		return
	}
	if n > 0 {
		r.out[table] += int(n)
		r.tx.TouchAll(topics)
	}
}

// endedPRs deletes the core's rows of the ended pull requests.
func (r *retainer) endedPRs(ended []pr.Ref) {
	if len(ended) == 0 {
		return
	}
	list := keyList(ended)
	for _, t := range endedTables {
		r.exec(t.table, t.topics, t.query, list)
	}
}

// unitless deletes the loop's rows of the github units not among prKeys,
// the bound list of the stored pull requests' unit keys.
func (r *retainer) unitless(before, prKeys string) {
	if r.err != nil {
		return
	}
	rows, err := r.tx.QueryContext(r.ctx, unitlessUnitsSQL, prKeys, before)
	if err != nil {
		r.err = err
		return
	}
	var units []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			r.err = err
			return
		}
		units = append(units, key)
	}
	if r.err = errors.Join(rows.Err(), rows.Close()); r.err != nil || len(units) == 0 {
		return
	}
	list, err := json.Marshal(units)
	if err != nil {
		r.err = err
		return
	}
	for _, t := range endedTables {
		if slices.Contains(unitlessTables, t.table) {
			r.exec(UnitlessLoopRowsKey, t.topics, t.query, string(list))
		}
	}
}

// retainInputs are what a pass reads of the plugins' reports before its
// own transaction: bound lists for the core's queries. A rule that reads
// which units a plugin still reports runs only once it reported any, so a
// store whose plugin has not loaded yet keeps its rows.
type retainInputs struct {
	ended, viewEnded []pr.Ref
	prKeys           []string
	githubReported   bool
	slackUnits       []string
	slackReported    bool
}

// slackPairs is the bound JSON list of the reported Slack units' keys.
func (in retainInputs) slackPairs() string { return jsonList(in.slackUnits) }

// jsonList is keys as a bound JSON list.
func jsonList(keys []string) string {
	raw, _ := json.Marshal(keys)
	return string(raw)
}

// keyList is the bound JSON list of refs' unit keys.
func keyList(refs []pr.Ref) string {
	keys := make([]string, len(refs))
	for i, ref := range refs {
		keys[i] = ref.String()
	}
	raw, _ := json.Marshal(keys)
	return string(raw)
}

// reportedUnits are the units the plugins report, as retention reads them.
type reportedUnits struct{ db *DB }

// endedPRs are the pull requests the GitHub plugin last reported merged or
// closed before before.
func (p reportedUnits) endedPRs(ctx context.Context, before string) ([]pr.Ref, error) {
	reported, err := p.keys(ctx, endedReportsSQL, before)
	if err != nil {
		return nil, err
	}
	var out []pr.Ref
	for _, key := range reported {
		ref, err := pr.ParseKey(key)
		if err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, nil
}

// keys are the first column of query's rows.
func (p reportedUnits) keys(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := p.db.R().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		out = append(out, key)
	}
	return out, rows.Err()
}

// The reports of a plugin's units: each key and whether the plugin dropped
// it.
const (
	reportedGitHubSQL = `SELECT unit_key, coalesce(json_extract(snapshot, '$.gone'), 0) FROM unit_snapshots WHERE provider = 'github'`
	reportedSlackSQL  = `SELECT unit_key, coalesce(json_extract(snapshot, '$.gone'), 0) FROM unit_snapshots WHERE provider = 'slack'`
)

// live are the unit keys a plugin reports, as query reads them, and has not
// dropped, and whether it reported any unit, dropped or not.
func (p reportedUnits) live(ctx context.Context, query string) (keys []string, reported bool, err error) {
	rows, err := p.db.R().QueryContext(ctx, query)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	keys = []string{}
	for rows.Next() {
		var key string
		var gone bool
		if err := rows.Scan(&key, &gone); err != nil {
			return nil, false, err
		}
		reported = true
		if !gone {
			keys = append(keys, key)
		}
	}
	return keys, reported, rows.Err()
}
