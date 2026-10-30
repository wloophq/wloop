package inventory

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func queryPlan(t *testing.T, s *Store, q string, args ...any) string {
	t.Helper()
	rows, err := s.db.R().QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+q, args...)
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
	// tempSorts are the sorts the statement keeps.
	tempSorts []string
}

var hotPlans = []hotPlan{
	{name: "open messages", query: messagesOpenSQL, want: []string{"messages_open (closed_at=?)"}},
	{name: "open question", query: openQuestionSQL, args: 5, want: []string{"messages_open (closed_at=?)"}},
	{name: "open workflow proposal", query: openWorkflowSQL, args: 4, want: []string{"messages_open (closed_at=?)"}},
	{name: "ledger message", query: ledgerMessageSQL, args: 1, want: []string{"messages_proposal (proposal=?)"}},
	{name: "recent activity", query: recentActivitySQL(marks(2)), args: 5, want: []string{"SCAN e USING INDEX events_recent"}},
	{name: "active refs", query: activeRefsSQL, args: 1, want: []string{"events_recent (at>?)"}, tempSorts: []string{"USE TEMP B-TREE FOR DISTINCT"}},
	{name: "timed turns", query: timedTurnsSQL, args: 1, want: []string{"turns_sent_at (sent_at>?)"}, tempSorts: []string{"USE TEMP B-TREE FOR ORDER BY"}},
	{name: "turn tokens", query: turnTokensSQL, args: 1, want: []string{"SEARCH t USING INTEGER PRIMARY KEY"}},
	{name: "last situation", query: lastSituationSQL, args: 3, want: []string{"events_unit_kind (provider=? AND unit_key=? AND kind=?)"}, tempSorts: []string{"USE TEMP B-TREE FOR ORDER BY"}},
	{name: "last event", query: lastEventSQL, args: 3, want: []string{"events_unit_kind (provider=? AND unit_key=? AND kind=?)"}},
	{name: "ended", query: endedSQL(marks(2)), args: 3, want: []string{"events_keyed"}},
	{name: "events of units", query: eventsByUnitsSQL, args: 1, want: []string{"events_unit (provider=? AND unit_key=?)"}, tempSorts: []string{"USE TEMP B-TREE FOR ORDER BY"}},
	{name: "outages", query: outagesSQL, args: 1, want: []string{"events_kind (kind=? AND at>?)"}},
	{name: "latest bundle", query: latestBundleSQL, args: 2, want: []string{"turn_instructions_kind (kind=? AND repo=?)"}},
	{name: "tag vocabulary", query: tagVocabularySQL(2), args: 4, want: []string{"events_kind (kind=?)"}, tempSorts: []string{"USE TEMP B-TREE FOR GROUP BY", "USE TEMP B-TREE FOR ORDER BY"}},
}

var (
	tempBTree = regexp.MustCompile(`USE TEMP B-TREE[^\n]*`)
	indexUse  = regexp.MustCompile(`INDEX (\w+)`)
)

func schemaTables(t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := s.db.R().QueryContext(context.Background(), `SELECT name FROM sqlite_schema WHERE type = 'table'`)
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

func TestHotPlans(t *testing.T) {
	s := open(t)
	tables := schemaTables(t, s)
	for _, p := range hotPlans {
		t.Run(p.name, func(t *testing.T) {
			plan := queryPlan(t, s, p.query, anyArgs(p.args)...)
			for _, w := range p.want {
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
				if !slices.Contains(p.tempSorts, sort) {
					t.Errorf("unexpected %q:\n%s", sort, plan)
				}
			}
			for _, sort := range p.tempSorts {
				if !strings.Contains(plan, sort) {
					t.Errorf("the allowed sort %q is gone:\n%s", sort, plan)
				}
			}
		})
	}
}

// indexesPlannedElsewhere are the indexes of the turn, stop, session handle, plan, task, project, draft and gate tables, whose
// statements the sqlite package's plans cover, and the unique index that
// only constrains INSERT OR IGNORE.
var indexesPlannedElsewhere = []string{
	"events_unkeyed",
	"turns_unit", "turns_turn_id", "turns_session", "turns_sent_to", "turns_open",
	"turn_records_once", "turn_records_unit", "turn_answers_unit", "turn_stops_session", "turn_stops_at", "intakes_open",
	"session_handles_handle", "session_handles_open",
	"plan_items_workflow",
	"tasks_state", "tasks_rev", "tasks_category", "task_items_unit", "task_units_unit",
	"projects_slug", "review_runs_open",
	"links_to",
	// pr_state is of the version 6 pull_requests, which the host no longer
	// reads.
	"pr_state",
	"drafts_state", "drafts_unit", "drafts_task", "drafts_rule", "drafts_workflow",
	"gates_due", "gates_task", "gates_unit",
}

func TestNoUnusedIndex(t *testing.T) {
	s := open(t)
	used := map[string]bool{}
	for _, p := range hotPlans {
		for _, m := range indexUse.FindAllStringSubmatch(queryPlan(t, s, p.query, anyArgs(p.args)...), -1) {
			used[m[1]] = true
		}
	}
	rows, err := s.db.R().QueryContext(context.Background(), `SELECT name FROM sqlite_schema WHERE type = 'index' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		if !used[name] && !slices.Contains(indexesPlannedElsewhere, name) {
			t.Errorf("index %s is in no hot plan", name)
		}
	}
}
