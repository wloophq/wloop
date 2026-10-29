package sqlite

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/domain/unit"

	"github.com/wloophq/wloop/internal/domain/workflow"
)

func queryPlan(t *testing.T, db *DB, q string, args ...any) string {
	t.Helper()
	rows, err := db.R().QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+q, args...)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, detail)
	}
	return strings.Join(lines, "\n")
}

func marks(n int) string { return strings.TrimSuffix(strings.Repeat("?,", n), ",") }

func anyArgs(n int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = "x"
	}
	return out
}

type hotPlan struct {
	name, query string
	args        int
	want        []string
	// tempSort is the one sort the statement keeps.
	tempSort string
}

var hotPlans = []hotPlan{
	{name: "open work of a dossier's unit", query: unitOpenSQL, args: 4, want: []string{"items_state (state=? AND provider=? AND unit_key=?)",
		"task_items_unit (provider=? AND unit_key=?)", "task_units_unit (provider=? AND unit_key=?)"}},
	{name: "book of a turn", query: bookOfTurn, args: 1, want: []string{"SEARCH turns USING INDEX turns_turn_id (turn_id=?)"}},
	{name: "book of a round and a handle", query: bookOfBoth, args: 3, want: []string{"turns_turn_id"}},
	{name: "book of a handle", query: bookOfHandle, args: 2, want: []string{"turns_session (session=?)", "turns_sent_to (sent_to=?)"}},
	{name: "units of handles", query: unitsOfSQL(2), args: 2, want: []string{"turns_session (session=?)", "turns_sent_to (sent_to=?)"}},
	{name: "every book: turns", query: fmt.Sprintf(bookTurnsSQL, bookWhere(0)), want: []string{"SCAN turns USING INDEX turns_unit"}},
	{name: "every book: records", query: fmt.Sprintf(bookRecordsSQL, bookWhere(0)), want: []string{"SCAN turn_records USING INDEX turn_records_unit"}},
	{name: "every book: answers", query: fmt.Sprintf(bookAnswersSQL, bookWhere(0)), want: []string{"SCAN turn_answers USING INDEX turn_answers_unit"}},
	{name: "books of units: turns", query: fmt.Sprintf(bookTurnsSQL, bookWhere(2)), args: 3, want: []string{"SEARCH turns USING INDEX turns_unit (provider=? AND unit_key=?)"}},
	{name: "books of units: records", query: fmt.Sprintf(bookRecordsSQL, bookWhere(2)), args: 3, want: []string{"SEARCH turn_records USING INDEX turn_records_unit (provider=? AND unit_key=?)"}},
	{name: "books of units: answers", query: fmt.Sprintf(bookAnswersSQL, bookWhere(2)), args: 3, want: []string{"SEARCH turn_answers USING INDEX turn_answers_unit (provider=? AND unit_key=?)"}},
	{name: "last stops", query: lastStopsSQL(marks(2)), args: 2, want: []string{"turn_stops_session"},
		tempSort: "USE TEMP B-TREE FOR LAST 2 TERMS OF ORDER BY"},
	{name: "session of a manager's handle", query: sessionOfSQL, args: 2, want: []string{"session_handles_handle (manager=? AND handle=?)"},
		tempSort: "USE TEMP B-TREE FOR ORDER BY"},
	{name: "open handle of a session", query: openHandleSQL, args: 1, want: []string{"session_handles_open (session_id=?)"}},
	{name: "open units", query: openUnitsSQL, want: []string{"turns_open"}, tempSort: "USE TEMP B-TREE FOR DISTINCT"},
	{name: "retain stops", query: retainStopsSQL, args: 1, want: []string{"SEARCH turn_stops USING INDEX turn_stops_at (at<?)"}},
	{name: "retain session events", query: retainSessionEventsSQL, args: len(SessionEventKinds) + 1, want: []string{"SEARCH events USING INDEX events_kind (kind=? AND at<?)"}},
	{name: "retain view events", query: retainViewEventsSQL, args: 4, want: []string{"SEARCH events USING INDEX events_kind (kind=? AND at<?)"}},
	{name: "retain an ended pull request's events", query: endedTables[5].query, args: 1, want: []string{"events_unit_kind (provider=? AND unit_key=?)"}},
	{name: "retain an ended pull request's turns", query: endedTables[1].query, args: 1, want: []string{"turns_unit (provider=? AND unit_key=?)"}},
	{name: "retain workflow events", query: retainWorkflowEventsSQL, args: len(workflow.EngineKinds) + 1, want: []string{"SEARCH events USING INDEX events_kind (kind=? AND at<?)"}},
	{name: "events since a cursor", query: eventsSinceSQL, args: 1, want: []string{"SEARCH events USING INTEGER PRIMARY KEY (rowid>?)"}},
	{name: "events of kinds since a cursor", query: eventsSinceKindsSQL, args: 2, want: []string{"SEARCH events USING INTEGER PRIMARY KEY (rowid>?)"}},
	{name: "replace a workflow's plan items", query: `DELETE FROM plan_items WHERE workflow = ?`, args: 1, want: []string{"SEARCH plan_items USING INDEX plan_items_workflow (workflow=?)"}},
	{name: "tasks in a category", query: `SELECT ` + taskFields + ` FROM tasks WHERE category = ?` + taskOrder, args: 1,
		want: []string{"tasks_category (category=?)"}, tempSort: "USE TEMP B-TREE FOR ORDER BY"},
	{name: "recategorize a state's tasks", query: recategorizeSQL, args: 4, want: []string{"tasks_state (state=?)"}},
	{name: "tasks changed since", query: `SELECT ` + taskFields + ` FROM tasks WHERE ` + changedSince + taskOrder, args: 1, want: []string{"tasks_rev (rev>?)"}, tempSort: "USE TEMP B-TREE FOR ORDER BY"},
	{name: "tasks of a unit", query: `SELECT ` + taskFields + ` FROM tasks WHERE ` + ofUnit + taskOrder, args: 4,
		want: []string{"task_units_unit (provider=? AND unit_key=?)", "task_items_unit (provider=? AND unit_key=?)"}, tempSort: "USE TEMP B-TREE FOR ORDER BY"},
	{name: "open work of a task's dossier", query: taskOpenSQL, args: 3, want: []string{"SEARCH tasks USING PRIMARY KEY (id=?)"}},
	{name: "the task holding a unit's dossier", query: holderSQL, args: 4,
		want: []string{"task_units_unit (provider=? AND unit_key=?)", "task_items_unit (provider=? AND unit_key=?)"}, tempSort: "USE TEMP B-TREE FOR ORDER BY"},
	{name: "the current review run", query: currentRunSQL, want: []string{"review_runs_open (finished_at=?)"}},
	{name: "a workflow's plan items", query: `SELECT ` + itemColumns + ` FROM plan_items WHERE workflow = ? ORDER BY id`, args: 1, want: []string{"plan_items_workflow (workflow=?)"}},
	{name: "due gates", query: gatesDueSQL, args: 2, want: []string{"gates_due (status=? AND deadline<?)"}, tempSort: "USE TEMP B-TREE FOR ORDER BY"},
	{name: "a task's gates", query: gatesOfTaskSQL, args: 1, want: []string{"gates_task (task_id=?)"}},
	gatesWherePlan("open gates", gates.Query{}, "gates_due (status=?)", "USE TEMP B-TREE FOR ORDER BY"),
	gatesWherePlan("gates of a unit", gates.Query{Unit: unit.Unit{Provider: "slack", Key: "T1/C1/1.1"}}, "gates_unit (provider=? AND unit_key=?)", ""),
}

func gatesWherePlan(name string, q gates.Query, want, tempSort string) hotPlan {
	query, args := gatesWhereSQL(q)
	return hotPlan{name: name, query: query, args: len(args), want: []string{want}, tempSort: tempSort}
}

var tempBTree = regexp.MustCompile(`USE TEMP B-TREE[^\n]*`)

func checkPlan(t *testing.T, plan string, want []string, tempSort string) {
	t.Helper()
	tables := schemaTables(t)
	for _, w := range want {
		if !strings.Contains(plan, w) {
			t.Errorf("plan lacks %q:\n%s", w, plan)
		}
	}
	for _, line := range strings.Split(plan, "\n") {
		target, ok := strings.CutPrefix(line, "SCAN ")
		if ok && !strings.Contains(line, "USING") && slices.Contains(tables, strings.Fields(target)[0]) {
			t.Errorf("full scan %q:\n%s", line, plan)
		}
	}
	for _, sort := range tempBTree.FindAllString(plan, -1) {
		if sort != tempSort {
			t.Errorf("unexpected %q:\n%s", sort, plan)
		}
	}
	if tempSort != "" && !strings.Contains(plan, tempSort) {
		t.Errorf("the allowed sort %q is gone:\n%s", tempSort, plan)
	}
}

func schemaTables(t *testing.T) []string {
	t.Helper()
	db, _ := openTemp(t)
	rows, err := db.R().QueryContext(context.Background(), `SELECT name FROM sqlite_schema WHERE type = 'table'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

var indexUse = regexp.MustCompile(`INDEX (\w+)`)

func usedIndexes(plan string) []string {
	var out []string
	for _, m := range indexUse.FindAllStringSubmatch(plan, -1) {
		out = append(out, m[1])
	}
	return out
}

func TestHotPlans(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	for _, p := range hotPlans {
		t.Run(p.name, func(t *testing.T) {
			t.Parallel()
			checkPlan(t, queryPlan(t, db, p.query, anyArgs(p.args)...), p.want, p.tempSort)
		})
	}
}

// indexesUsedByInventory are the indexes of the tables the inventory store
// reads; its TestHotPlans covers them.
var indexesUsedByInventory = []string{
	"events_keyed", "events_unkeyed", "events_unit", "events_unit_kind", "events_recent", "events_kind",
	"turns_sent_at", "turn_instructions_kind", "turn_records_once", "intakes_open",
	"messages_open", "messages_task", "messages_proposal",
}

func TestNoUnusedIndex(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	used := map[string]bool{}
	for _, p := range hotPlans {
		for _, name := range usedIndexes(queryPlan(t, db, p.query, anyArgs(p.args)...)) {
			used[name] = true
		}
	}
	rows, err := db.R().QueryContext(context.Background(), `SELECT name FROM sqlite_schema WHERE type = 'index' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		if !used[name] && !slices.Contains(indexesUsedByInventory, name) {
			t.Errorf("index %s is in no hot plan", name)
		}
	}
}
