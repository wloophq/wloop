package sqlite

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/task"
)

const (
	hexPR      = "00000000000000a1"
	hexStart   = "00000000000000a2"
	hexCapture = "00000000000000a3"
	hexThread  = "00000000000000a4"
	hexProject = "00000000000000a5"
	hexChild   = "00000000000000a6"
)

var (
	v6FocusAt  = time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	v6Deadline = time.Date(2026, 10, 30, 18, 0, 0, 0, time.UTC)
	v6Reviewed = time.Date(2026, 10, 19, 8, 0, 0, 0, time.UTC)
	v6Updated  = time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)
)

// v6PropsFixture is a version 6 store holding a derived action on a pull request with every user field set, a
// capture a start turn ran on, a plain capture whose star the user cleared, a derived action on a thread held
// through its items, and a sequential project with its review and a child.
func v6PropsFixture(t *testing.T) string {
	t.Helper()
	ts := eventTime
	up := ts(v6Updated)
	return v6Store(t,
		`INSERT INTO entries (id, kind, state, area, focus_on, focus_auto, focus_at, deadline, origin, created, updated, rev) VALUES
			('e-`+hexPR+`', 'action', 'next', 'OSS', 1, 'deadline', '`+ts(v6FocusAt)+`', '`+ts(v6Deadline)+`', 'derived', '`+up+`', '`+up+`', 1),
			('e-`+hexStart+`', 'capture', 'inbox', '', 1, '', '`+ts(v6FocusAt)+`', '`+ts(v6Deadline)+`', 'user', '`+up+`', '`+up+`', 2),
			('e-`+hexCapture+`', 'capture', 'someday', '', 0, '', '`+ts(v6FocusAt)+`', '', 'user', '`+up+`', '`+up+`', 3),
			('e-`+hexThread+`', 'action', 'trash', '', 0, '', '', '', 'derived', '`+up+`', '`+up+`', 4)`,
		`INSERT INTO entries (id, kind, state, sequential, origin, created, updated, rev) VALUES
			('e-`+hexProject+`', 'project', 'next', 1, 'user', '`+up+`', '`+up+`', 5)`,
		`INSERT INTO entries (id, kind, state, project, position, origin, created, updated, rev) VALUES
			('e-`+hexChild+`', 'capture', 'reference', 'e-`+hexProject+`', 2, 'user', '`+up+`', '`+up+`', 6)`,
		`INSERT INTO entry_units (entry_id, provider, unit_key, ord) VALUES ('e-`+hexPR+`', 'github', 'acme/api#7', 0)`,
		`INSERT INTO entry_items (entry_id, provider, unit_key, item) VALUES ('e-`+hexThread+`', 'slack', 'T1/C1/1.1', 'i1')`,
		`INSERT INTO entry_contexts (entry_id, context, ord) VALUES ('e-`+hexPR+`', '@reply', 0), ('e-`+hexPR+`', '@decide', 1),
			('e-`+hexCapture+`', '@calls', 0)`,
		`INSERT INTO projects (entry_id, slug, review_every, reviewed_at) VALUES ('e-`+hexProject+`', 'ship-it', 1209600, '`+ts(v6Reviewed)+`')`,
		`INSERT INTO turns (seq, provider, unit_key, intent, turn_id, session, state, sent_at, inputs, runner) VALUES
			(1, 'entry', 'e-`+hexStart+`', 'start', 'r1', 's1', 'ended', '`+up+`', '{}', '')`,
	)
}

func migratedTask(t *testing.T, db *DB, hex string) task.Task {
	t.Helper()
	e, err := db.Tasks().Task(context.Background(), task.ID("t-"+hex))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestV7PropsMovesUserFields(t *testing.T) {
	t.Parallel()
	path := v6PropsFixture(t)
	db := migrateV6(t, path)
	pr := migratedTask(t, db, hexPR)
	want := map[string]task.Written{
		"area":     {By: task.WriterPerson, At: v6Updated},
		"focus":    {By: task.WriterWorkflow, At: v6FocusAt},
		"autostar": {By: task.WriterWorkflow, At: v6FocusAt},
		"due":      {By: task.WriterPlugin, At: v6Updated},
		"contexts": {By: task.WriterPlugin, At: v6Updated},
		"kind":     {By: task.WriterPlugin, At: v6Updated},
	}
	for name, w := range want {
		if got := pr.Written[name]; got.By != w.By || !got.At.Equal(w.At) {
			t.Errorf("%s written %+v, want %+v", name, got, w)
		}
	}
	if pr.Prop("area").Text != "OSS" || !pr.Starred() || !pr.Due().Equal(v6Deadline) ||
		!slices.Equal(pr.Prop("contexts").List, []string{"@reply", "@decide"}) || len(pr.Props) != len(want) {
		t.Errorf("the pull request's properties %+v", pr.Props)
	}
	start := migratedTask(t, db, hexStart)
	if !start.Starred() || start.Written["focus"].By != task.WriterPerson || start.Written["due"].By != task.WriterPerson {
		t.Errorf("a user's star and deadline %+v %+v", start.Props, start.Written)
	}
	capture := migratedTask(t, db, hexCapture)
	if f := capture.Prop("focus"); f.Text != task.FocusOff || capture.Written["focus"] != (task.Written{By: task.WriterPerson, At: v6FocusAt}) {
		t.Errorf("a star the user cleared: %+v %+v", f, capture.Written["focus"])
	}
	if capture.Written["contexts"].By != task.WriterPerson || !slices.Equal(capture.Prop("contexts").List, []string{"@calls"}) {
		t.Errorf("a capture's contexts %+v", capture.Props)
	}
	if _, ok := capture.Props["due"]; ok {
		t.Errorf("a task with no deadline has a due: %+v", capture.Props)
	}
	raw := rawDB(t, path)
	if got := strings1(t, raw, `SELECT name FROM sqlite_schema WHERE name = 'task_contexts'`); len(got) != 0 {
		t.Errorf("task_contexts is kept: %v", got)
	}
	for _, col := range []string{"focus_on", "focus_auto", "focus_at", "area", "deadline", "project"} {
		if got := strings1(t, raw, `SELECT name FROM pragma_table_info('tasks') WHERE name = ?`, col); len(got) != 0 {
			t.Errorf("tasks keeps %s", col)
		}
	}
	if got := strings1(t, raw, `SELECT id || ' ' || category FROM tasks ORDER BY id`); !slices.Equal(got, []string{
		"t-" + hexPR + " ready", "t-" + hexStart + " inbox", "t-" + hexCapture + " deferred", "t-" + hexThread + " dropped",
		"t-" + hexProject + " ready", "t-" + hexChild + " done"}) {
		t.Errorf("categories %q", got)
	}
}

func TestV7PropsSetsKindFromOrigin(t *testing.T) {
	t.Parallel()
	db := migrateV6(t, v6PropsFixture(t))
	for hex, want := range map[string]task.Written{
		hexPR: {By: task.WriterPlugin}, hexThread: {By: task.WriterPlugin}, hexStart: {By: task.WriterWorkflow},
		hexCapture: {By: task.WriterWorkflow}, hexProject: {By: task.WriterWorkflow},
	} {
		if got := migratedTask(t, db, hex).Written["kind"].By; got != want.By {
			t.Errorf("t-%s kind written by %s, want %s", hex, got, want.By)
		}
	}
	for hex, want := range map[string]string{hexPR: "pull-request", hexThread: "thread", hexStart: "start", hexCapture: "capture",
		hexProject: "project", hexChild: "capture"} {
		if got := migratedTask(t, db, hex).Prop("kind").Text; got != want {
			t.Errorf("t-%s kind %q, want %q", hex, got, want)
		}
	}
}

func TestV7PropsKeepsProjectsAsParents(t *testing.T) {
	t.Parallel()
	path := v6PropsFixture(t)
	db := migrateV6(t, path)
	p := migratedTask(t, db, hexProject)
	if p.ReviewEvery != 14*24*time.Hour || !p.ReviewedAt.Equal(v6Reviewed) || !p.Sequential {
		t.Errorf("the project %+v", p)
	}
	child := migratedTask(t, db, hexChild)
	if child.Parent != p.ID || child.Position != 2 || child.State != "reference" || child.Category != task.CategoryDone {
		t.Errorf("the child %+v", child)
	}
	raw := rawDB(t, path)
	if got := strings1(t, raw, `SELECT review_every || ' ' || reviewed_at FROM tasks WHERE id = ?`, "t-"+hexChild); !slices.Equal(got, []string{"0 "}) {
		t.Errorf("a child's review %q", got)
	}
}
