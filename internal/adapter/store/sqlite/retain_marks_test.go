package sqlite

import (
	"maps"
	"slices"
	"testing"
)

func TestRetainDropsTheMarksOfEndedTasksAndVanishedSlackUnits(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	for id, c := range map[string]string{"e-open": "waiting", "e-done": "done", "e-trash": "dropped", "e-ref": "deferred"} {
		exec(t, db, `INSERT INTO tasks (id, kind, state, category, origin, created, updated, rev) VALUES (?, 'action', ?, ?, 'user', ?, ?, 1)`, id, "s-"+id, c, at(past), at(past))
	}
	exec(t, db, `INSERT INTO extraction_cursors (provider, unit_key, at) VALUES ('slack', 'T1/C1/1.1', ?)`, at(past))
	exec(t, db, `INSERT INTO items (provider, unit_key, id, body, state, provenance, created_at, updated_at) VALUES ('slack', 'T1/C1/2.2', 'i1', '{}', 'open', 'extracted', ?, ?)`, at(past), at(past))
	exec(t, db, `INSERT INTO task_units (task_id, provider, unit_key, ord) VALUES ('e-open', 'slack', 'T1/C1/3.3', 0)`)
	marks := map[string][]string{
		"task":  {"e-open", "e-done", "e-trash", "e-ref", "e-gone"},
		"slack": {"T1/C1/1.1", "T1/C1/2.2", "T1/C1/3.3", "T1/C1/9.9"},
		"chat":  {"kept"},
	}
	for p, keys := range marks {
		for _, k := range keys {
			exec(t, db, `INSERT INTO unit_marks (provider, unit_key, mark, value) VALUES (?, ?, 'w/settled', 'v')`, p, k)
		}
	}
	got := retain(t, db, Retention{SubjectMarks: true})
	if want := (Retained{"unit_marks": 4}); !maps.Equal(got, want) {
		t.Errorf("retained %v, want %v", got, want)
	}
	left := column(t, db, `SELECT provider || ':' || unit_key FROM unit_marks ORDER BY 1`)
	if want := []string{"chat:kept", "slack:T1/C1/1.1", "slack:T1/C1/2.2", "slack:T1/C1/3.3", "task:e-open", "task:e-ref"}; !slices.Equal(left, want) {
		t.Errorf("marks left %q, want %q", left, want)
	}
	if again := retain(t, db, Retention{SubjectMarks: true}); len(again) != 0 {
		t.Errorf("a second pass retained %v", again)
	}
	if !DefaultRetention.SubjectMarks {
		t.Error("the retain job keeps the marks of ended entries")
	}
}
