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
	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// Retention says how long each kind of row is kept. Zero keeps forever.
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
	VerbEvents     time.Duration // the verbs people ran on entries, gtd.VerbEventKinds
	// UnitlessLoopRows drops the turns, records, answers, intakes, events,
	// workflow runs and plan items of a github unit with no pull_requests
	// row, no overrides row and nothing newer.
	UnitlessLoopRows time.Duration
	Classifications  time.Duration // classifications and classify_days
	Fetches          time.Duration
	// SubjectMarks drops the marks of entries done, in the trash or gone,
	// and of Slack units with no extraction cursor, no item and no entry.
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
	VerbEvents:       retentionWindow,
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
	VerbEventsKey       = "verb_events"
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

// endedAt is when a pull request row ended, empty for one that has not;
// times in event.TimeLayout sort as text.
const endedAt = `max(merged_at, closed_at)`

const (
	retainViewEventsSQL = `DELETE FROM events WHERE kind IN (?, ?, ?) AND at < ?`
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
	retainViewRowsSQL = `DELETE FROM view_applied WHERE provider = 'github' AND unit_key IN
		(SELECT owner || '/' || repo || '#' || number FROM pull_requests WHERE state != 'OPEN' AND ` + endedAt + ` != '' AND ` + endedAt + ` < ?)`
	endedPRsSQL           = `SELECT owner, repo, number FROM pull_requests WHERE state != 'OPEN' AND ` + endedAt + ` != '' AND ` + endedAt + ` < ?`
	retainPlanItemsSQL    = `DELETE FROM plan_items WHERE decision != 'pending' AND saved_at < ?`
	retainWorkflowRunsSQL = `DELETE FROM workflow_runs WHERE status = 'done' AND at < ?`
	retainSessionRunsSQL  = `DELETE FROM workflow_runs WHERE provider = 'session' AND at < ? AND unit_key NOT IN (SELECT session_id FROM sessions)`
	retainSessionMarksSQL = `DELETE FROM unit_marks WHERE provider = 'session' AND unit_key NOT IN (SELECT session_id FROM sessions)`
	retainSubjectMarksSQL = `DELETE FROM unit_marks WHERE
		(provider = 'entry' AND unit_key NOT IN (SELECT id FROM entries WHERE state NOT IN ('done', 'trash')))
		OR (provider = 'slack'
			AND NOT EXISTS (SELECT 1 FROM extraction_cursors c WHERE c.provider = 'slack' AND c.unit_key = unit_marks.unit_key)
			AND NOT EXISTS (SELECT 1 FROM items i WHERE i.provider = 'slack' AND i.unit_key = unit_marks.unit_key)
			AND NOT EXISTS (SELECT 1 FROM entry_units u WHERE u.provider = 'slack' AND u.unit_key = unit_marks.unit_key))`
	retainClassificationsSQL = `DELETE FROM classifications WHERE at < ?`
	retainClassifyDaysSQL    = `DELETE FROM classify_days WHERE day < ?`
	retainFetchesSQL         = `DELETE FROM fetches WHERE at < ?`
	unitlessUnitsSQL         = `SELECT unit_key FROM (
		SELECT unit_key, max(sent_at, started_at, ended_at) AS t FROM turns WHERE provider = 'github'
		UNION ALL SELECT unit_key, at FROM turn_records WHERE provider = 'github'
		UNION ALL SELECT unit_key, at FROM turn_answers WHERE provider = 'github'
		UNION ALL SELECT unit_key, max(accepted_at, briefing_at, starting_at, ended_at) FROM intakes WHERE provider = 'github'
		UNION ALL SELECT unit_key, at FROM events WHERE provider = 'github'
		UNION ALL SELECT unit_key, at FROM workflow_runs WHERE provider = 'github'
		UNION ALL SELECT unit_key, saved_at FROM plan_items WHERE provider = 'github'
	) WHERE unit_key NOT IN (SELECT owner || '/' || repo || '#' || number FROM pull_requests)
		AND unit_key NOT IN (SELECT unit_key FROM overrides WHERE provider = 'github')
	GROUP BY unit_key HAVING max(t) != '' AND max(t) < ?`
)

// A step that ran on a unit of work lives as long as that unit's events.
var retainWorkflowEventsSQL = `DELETE FROM events WHERE kind IN (` + strings.TrimSuffix(strings.Repeat("?, ", len(workflow.EngineKinds)), ", ") + `) AND at < ?
	AND NOT (kind = '` + workflow.KindStepRan + `' AND provider NOT IN ('', '` + string(unit.Session) + `', '` + string(unit.Workspace) + `'))`

var retainVerbEventsSQL = `DELETE FROM events WHERE kind IN (` + strings.TrimSuffix(strings.Repeat("?, ", len(gtd.VerbEventKinds)), ", ") + `) AND at < ?`

// unitlessTables are the endedTables UnitlessLoopRows deletes from.
var unitlessTables = []string{"turn_usage", "turns", "turn_records", "turn_answers", "intakes", "events", "workflow_runs", "plan_items", "unit_marks", "round_state"}

// Session-keyed bookkeeping is stored under its session unit, the rest
// under no unit; a pull request's events never match.
var retainSessionEventsSQL = `DELETE FROM events WHERE kind IN (` + strings.TrimSuffix(strings.Repeat("?, ", len(SessionEventKinds)), ", ") +
	`) AND at < ? AND +provider IN ('', 'session')`

// endedUnits and endedRefs read the bound JSON list of the ended pull
// requests' unit keys, or of their [owner, repo, number] triples.
const (
	endedUnits = `(SELECT value FROM json_each(?))`
	endedRefs  = `(SELECT json_extract(value, '$[0]'), json_extract(value, '$[1]'), json_extract(value, '$[2]') FROM json_each(?))`
)

type endedTable struct {
	table  string
	topics port.Topic
	query  string
	byRef  bool // bound to the triples; otherwise to the unit keys
}

// endedTables is in delete order: turn_usage goes before the turns it hangs
// from, pull_requests last.
var endedTables = slices.Concat([]endedTable{
	{"turn_usage", port.TopicUsage, `DELETE FROM turn_usage WHERE turn_seq IN (SELECT seq FROM turns WHERE provider = 'github' AND unit_key IN ` + endedUnits + `)`, false},
	{"turns", port.TopicTurns, `DELETE FROM turns WHERE provider = 'github' AND unit_key IN ` + endedUnits, false},
	{"turn_records", port.TopicTurns, `DELETE FROM turn_records WHERE provider = 'github' AND unit_key IN ` + endedUnits, false},
	{"turn_answers", port.TopicTurns, `DELETE FROM turn_answers WHERE provider = 'github' AND unit_key IN ` + endedUnits, false},
	{"intakes", port.TopicIntakes, `DELETE FROM intakes WHERE provider = 'github' AND unit_key IN ` + endedUnits, false},
	{"events", port.TopicSourceEvents | port.TopicLoopEvents, `DELETE FROM events WHERE provider = 'github' AND unit_key IN ` + endedUnits, false},
	{"overrides", port.TopicOverrides, `DELETE FROM overrides WHERE provider = 'github' AND unit_key IN ` + endedUnits, false},
	{"workflow_runs", 0, `DELETE FROM workflow_runs WHERE provider = 'github' AND unit_key IN ` + endedUnits, false},
	{"plan_items", port.TopicGate, `DELETE FROM plan_items WHERE provider = 'github' AND unit_key IN ` + endedUnits, false},
	{"unit_marks", port.TopicRounds, `DELETE FROM unit_marks WHERE provider = 'github' AND unit_key IN ` + endedUnits, false},
	{"round_state", port.TopicRounds, `DELETE FROM round_state WHERE provider = 'github' AND unit_key IN ` + endedUnits, false},
	{"view_applied", port.TopicViews, `DELETE FROM view_applied WHERE provider = 'github' AND unit_key IN ` + endedUnits, false},
}, briefEndedTables, []endedTable{
	{"pr_seen", 0, `DELETE FROM pr_seen WHERE (owner, repo, number) IN ` + endedRefs, true},
	{"pr_details", port.TopicPRs, `DELETE FROM pr_details WHERE (owner, repo, number) IN ` + endedRefs, true},
	{"pull_requests", port.TopicPRs | port.TopicFollow, `DELETE FROM pull_requests WHERE (owner, repo, number) IN ` + endedRefs, true},
})

// Retain applies p as of now in one write transaction, noting Change.All
// with the topics of the tables it changed, then runs incremental_vacuum and
// PRAGMA optimize outside it. EndedPRs runs first, so the rules after it see
// the sessions and runs only an ended pull request's turns held, and a
// second pass at the same now changes nothing and notes nothing.
func (db *DB) Retain(ctx context.Context, now time.Time, p Retention) (Retained, error) {
	var out Retained
	_, err := db.Write(ctx, func(tx *Tx) error {
		out = Retained{}
		r := &retainer{ctx: ctx, tx: tx, out: out}
		if p.EndedPRs > 0 {
			r.endedPRs(cutoff(now, p.EndedPRs))
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
			r.briefs(cutoff(now, p.BriefBodies))
		}
		if p.ViewRows > 0 {
			r.exec("view_applied", port.TopicViews, retainViewRowsSQL, cutoff(now, p.ViewRows))
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
		if p.VerbEvents > 0 {
			args := make([]any, 0, len(gtd.VerbEventKinds)+1)
			for _, k := range gtd.VerbEventKinds {
				args = append(args, k)
			}
			r.exec(VerbEventsKey, port.TopicLoopEvents, retainVerbEventsSQL, append(args, cutoff(now, p.VerbEvents))...)
		}
		if p.UnitlessLoopRows > 0 {
			r.unitless(cutoff(now, p.UnitlessLoopRows))
		}
		if p.Classifications > 0 {
			r.exec("classifications", 0, retainClassificationsSQL, cutoff(now, p.Classifications))
			r.exec("classify_days", 0, retainClassifyDaysSQL, now.Add(-p.Classifications).UTC().Format(time.DateOnly))
		}
		if p.Fetches > 0 {
			r.exec("fetches", 0, retainFetchesSQL, cutoff(now, p.Fetches))
		}
		if p.SubjectMarks {
			r.exec("unit_marks", port.TopicMarks, retainSubjectMarksSQL)
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

func (r *retainer) endedPRs(before string) {
	rows, err := r.tx.QueryContext(r.ctx, endedPRsSQL, before)
	if err != nil {
		r.err = err
		return
	}
	var units []string
	var refs [][3]any
	for rows.Next() {
		var ref pr.Ref
		if err := rows.Scan(&ref.Owner, &ref.Repo, &ref.Number); err != nil {
			rows.Close()
			r.err = err
			return
		}
		units = append(units, ref.String())
		refs = append(refs, [3]any{ref.Owner, ref.Repo, ref.Number})
	}
	if r.err = errors.Join(rows.Err(), rows.Close()); r.err != nil || len(units) == 0 {
		return
	}
	unitList, err := json.Marshal(units)
	if err != nil {
		r.err = err
		return
	}
	refList, err := json.Marshal(refs)
	if err != nil {
		r.err = err
		return
	}
	for _, t := range endedTables {
		list := unitList
		if t.byRef {
			list = refList
		}
		r.exec(t.table, t.topics, t.query, string(list))
	}
}

func (r *retainer) unitless(before string) {
	if r.err != nil {
		return
	}
	rows, err := r.tx.QueryContext(r.ctx, unitlessUnitsSQL, before)
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
