package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/mark"
	"github.com/wloophq/wloop/internal/domain/unit"
)

const (
	hexA = "0a1b2c3d4e5f6a7b"
	hexP = "1111222233334444"
	hexB = "9999888877776666"
)

type schemaColumn struct {
	name, typ, dflt string
	notNull         bool
	pk              int
}

// liveTables are the tables of the store at path with their columns, by name.
func liveTables(t *testing.T, db *sql.DB) map[string][]schemaColumn {
	t.Helper()
	rows, err := db.Query(`SELECT m.name, p.name, p.type, coalesce(p.dflt_value, ''), p."notnull", p.pk
		FROM sqlite_schema m JOIN pragma_table_info(m.name) p
		WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite_%' ORDER BY m.name, p.cid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string][]schemaColumn{}
	for rows.Next() {
		var table string
		var c schemaColumn
		if err := rows.Scan(&table, &c.name, &c.typ, &c.dflt, &c.notNull, &c.pk); err != nil {
			t.Fatal(err)
		}
		out[table] = append(out[table], c)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// insertRow writes one row into table with vals, every other NOT NULL column without a default set to a
// placeholder of its type; an INTEGER PRIMARY KEY is left to SQLite.
func insertRow(t *testing.T, db *sql.DB, table string, cols []schemaColumn, vals map[string]string) {
	t.Helper()
	insertRowAs(t, db, table, cols, vals, "")
}

// insertRowAs is insertRow with tag ending every text placeholder, so rows of one table differ.
func insertRowAs(t *testing.T, db *sql.DB, table string, cols []schemaColumn, vals map[string]string, tag string) {
	t.Helper()
	var names, marks []string
	var args []any
	for _, c := range cols {
		v, ok := vals[c.name]
		switch {
		case ok:
			args = append(args, v)
		case c.pk == 1 && strings.EqualFold(c.typ, "INTEGER") && len(pkOf(cols)) == 1:
			continue
		case !c.notNull || c.dflt != "":
			continue
		case c.name == "record" || c.name == "body":
			args = append(args, "{}")
		case strings.EqualFold(c.typ, "INTEGER") || strings.EqualFold(c.typ, "REAL"):
			args = append(args, 1)
		default:
			args = append(args, "x-"+c.name+tag)
		}
		names = append(names, c.name)
		marks = append(marks, "?")
	}
	q := `INSERT INTO ` + table + ` (` + strings.Join(names, ", ") + `) VALUES (` + strings.Join(marks, ", ") + `)`
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%s %v: %v", q, args, err)
	}
}

func pkOf(cols []schemaColumn) []string {
	var out []string
	for _, c := range cols {
		if c.pk > 0 {
			out = append(out, c.name)
		}
	}
	return out
}

// v6Store is a store at schema version 6, from its frozen schema, with stmts run on it.
func v6Store(t *testing.T, stmts ...string) string {
	t.Helper()
	frozen, err := os.ReadFile(filepath.Join("testdata", "schema-v6.sql"))
	if err != nil {
		t.Fatal(err)
	}
	path := storeAt(t, schemaSet{6, string(frozen), nil})
	rawExec(t, path, stmts...)
	return path
}

func rawDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := handle(path+"?_pragma=busy_timeout(5000)", 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// v6Fixture is a version 6 store holding entries e-A and e-P (a project) with a row of every table that has a
// provider column on entry e-A, and the rows the rename tests read.
func v6Fixture(t *testing.T) string {
	t.Helper()
	a, p := "e-"+hexA, "e-"+hexP
	path := v6Store(t,
		`INSERT INTO entries (id, kind, state, project, origin, created, updated, rev) VALUES ('`+a+`', 'action', 'waiting', '`+p+`', 'user', 't', 't', 1)`,
		`INSERT INTO entries (id, kind, state, origin, created, updated, rev) VALUES ('`+p+`', 'project', 'next', 'user', 't', 't', 2)`,
		`INSERT INTO entry_contexts (entry_id, context, ord) VALUES ('`+a+`', '@reply', 0)`,
		`INSERT INTO entry_position (only, epoch, rev) VALUES (1, 'ep', 2)`,
		`INSERT INTO projects (entry_id, slug) VALUES ('`+p+`', 'ship-it')`,
		`INSERT INTO dossiers (owner, rel_path, status) VALUES ('entry/`+a+`', 'entry/`+a+`#', 'live')`,
		`INSERT INTO turns (seq, provider, unit_key, intent, turn_id, session, state, sent_at, inputs, runner) VALUES
			(1, 'entry', '`+a+`', 'start', 'r1', 's1', 'asked', 't', '{"depth":"deep","entry":"`+a+`"}', ''),
			(2, 'slack', 'T1/C1/1.1', 'act', 'r2', '', 'ended', 't', '{"entry":"`+a+`","items":["i1"]}', 'headless')`,
		`INSERT INTO messages (id, entry_id, provider, unit_key, kind, text, origin_kind, turn_seq, session, state, created_at) VALUES
			(10, '`+a+`', 'entry', '`+a+`', 'question', 'Ship it?', 'turn', 1, 's1', 'open', 't'),
			(11, '`+a+`', 'slack', 'T1/C1/1.1', 'post', 'Bruno asked', 'agent', 2, '', 'open', 't')`,
		`INSERT INTO drafts (id, provider, unit_key, entry, kind, origin_kind, workflow, rule, rule_key, state, created_at, updated_at, effects, origin_provider, origin_unit_key) VALUES
			(20, 'slack', 'T1/C1/1.1', '`+a+`', 'nudge', 'auto', 'stalled', 'stalled', 'entry:`+a+`', 'proposed', 't', 't', '["entry.nudged","unit.closed"]', 'entry', '`+a+`'),
			(21, 'slack', 'T1/C1/1.1', '`+a+`', 'nudge', 'auto', 'stalled', 'stalled', '`+a+`:chat:U1', 'sent', 't', 't', '["entry.nudged"]', '', '')`,
		`INSERT INTO links (provider, unit_key, to_provider, to_key, kind, evidence, origin, at) VALUES ('slack', 'T1/C1/1.1', 'entry', '`+a+`', 'mentions', 'e', 'user', 't')`,
	)
	db := rawDB(t, path)
	done := map[string]bool{"turns": true, "messages": true, "drafts": true, "links": true}
	for table, cols := range liveTables(t, db) {
		if done[table] || !slices.ContainsFunc(cols, func(c schemaColumn) bool { return c.name == "provider" }) {
			continue
		}
		vals := map[string]string{"provider": "entry"}
		for _, c := range cols {
			switch c.name {
			case "unit_key", "entry_id":
				vals[c.name] = a
			}
		}
		insertRow(t, db, table, cols, vals)
	}
	return path
}

func migrateV6(t *testing.T, path string) *DB {
	t.Helper()
	if _, err := current.migrate(context.Background(), path, migrateOpts(path)); err != nil {
		t.Fatal(err)
	}
	db, err := current.open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func strings1(t *testing.T, db *sql.DB, q string, args ...any) []string {
	t.Helper()
	rows, err := db.Query(q, args...)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s sql.NullString
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s.String)
	}
	return out
}

func TestV7RenameLeavesNoEntryReference(t *testing.T) {
	t.Parallel()
	path := v6Fixture(t)
	migrateV6(t, path)
	db := rawDB(t, path)
	for _, n := range strings1(t, db, `SELECT name FROM sqlite_schema WHERE name LIKE '%entr%'`) {
		t.Errorf("the store keeps %s", n)
	}
	for table, cols := range liveTables(t, db) {
		for _, c := range cols {
			for _, bad := range []string{"e-" + hexA, "e-" + hexP, `"entry"`, "entry:", "entry/", "entry."} {
				if n := strings1(t, db, `SELECT count(*) FROM `+table+` WHERE instr(`+c.name+`, ?) > 0`, bad); n[0] != "0" {
					t.Errorf("%s.%s holds %q in %s rows", table, c.name, bad, n[0])
				}
			}
			if c.name == "provider" || strings.HasSuffix(c.name, "_provider") {
				if n := strings1(t, db, `SELECT count(*) FROM `+table+` WHERE `+c.name+` = 'entry'`); n[0] != "0" {
					t.Errorf("%s.%s names the entry provider", table, c.name)
				}
				if n := strings1(t, db, `SELECT count(*) FROM `+table+` WHERE `+c.name+` = ?`, string(unit.Task)); !(c.name == "provider" && (table == "drafts" || table == "links")) && n[0] == "0" {
					t.Errorf("%s.%s lost its task row", table, c.name)
				}
			}
		}
	}
	if got := strings1(t, db, `SELECT id || ' ' || project FROM tasks ORDER BY id`); !slices.Equal(got, []string{"t-" + hexA + " t-" + hexP, "t-" + hexP + " "}) {
		t.Errorf("tasks %q", got)
	}
	if got := strings1(t, db, `SELECT task_id FROM projects`); !slices.Equal(got, []string{"t-" + hexP}) {
		t.Errorf("projects %q", got)
	}
	if got := strings1(t, db, `SELECT owner || ' ' || rel_path FROM dossiers`); !slices.Equal(got, []string{"task/t-" + hexA + " task/t-" + hexA + "#"}) {
		t.Errorf("dossiers %q", got)
	}
	if got := strings1(t, db, `SELECT inputs FROM turns ORDER BY seq`); !slices.Equal(got, []string{`{"depth":"deep","task":"t-` + hexA + `"}`, `{"items":["i1"],"task":"t-` + hexA + `"}`}) {
		t.Errorf("turn inputs %q", got)
	}
}

func TestV7RenameKeepsAnOpenQuestionRoutable(t *testing.T) {
	t.Parallel()
	path := v6Fixture(t)
	db := migrateV6(t, path)
	ctx := context.Background()
	tk := unit.Unit{Provider: unit.Task, Key: "t-" + hexA}
	var id int64
	var taskID, session string
	if err := db.R().QueryRowContext(ctx, `SELECT id, task_id, session FROM messages WHERE state = 'open' AND provider = ? AND unit_key = ?
		AND origin_kind = 'turn' AND turn_seq = 1 AND kind = 'question'`, string(tk.Provider), tk.Key).Scan(&id, &taskID, &session); err != nil {
		t.Fatalf("the open question on %s: %v", tk, err)
	}
	if id != 10 || taskID != tk.Key || session != "s1" {
		t.Fatalf("message %d of task %q on session %q", id, taskID, session)
	}
	books, err := New(db).Books(ctx, tk)
	if err != nil {
		t.Fatal(err)
	}
	b := books[tk]
	if len(b.Turns) != 1 || b.Turns[0].Seq != 1 || b.Turns[0].Session != "s1" || string(b.Turns[0].State) != "asked" {
		t.Fatalf("the turn the question waits on: %+v", b.Turns)
	}
	if got := strings1(t, rawDB(t, path), `SELECT task_id FROM messages WHERE id = 11`); !slices.Equal(got, []string{tk.Key}) {
		t.Errorf("the agent post's task %q", got)
	}
}

func TestV7RenameRewritesNudgedEffects(t *testing.T) {
	t.Parallel()
	path := v6Fixture(t)
	migrateV6(t, path)
	got := strings1(t, rawDB(t, path), `SELECT effects FROM drafts ORDER BY id`)
	if want := []string{`["task.nudged","unit.closed"]`, `["task.nudged"]`}; !slices.Equal(got, want) {
		t.Errorf("effects %q, want %q", got, want)
	}
}

func TestV7RenameRewritesIdsInDraftKeys(t *testing.T) {
	t.Parallel()
	path := v6Fixture(t)
	migrateV6(t, path)
	got := strings1(t, rawDB(t, path), `SELECT task || ' ' || rule_key || ' ' || origin_provider || ':' || origin_unit_key FROM drafts ORDER BY id`)
	a := "t-" + hexA
	if want := []string{a + " task:" + a + " task:" + a, a + " " + a + ":chat:U1 :"}; !slices.Equal(got, want) {
		t.Errorf("drafts %q, want %q", got, want)
	}
}

func TestRetryingAnOldHeadlessTurnFindsItsTask(t *testing.T) {
	t.Parallel()
	db := migrateV6(t, v6Fixture(t))
	u := unit.Unit{Provider: "slack", Key: "T1/C1/1.1"}
	books, err := New(db).Books(context.Background(), u)
	if err != nil {
		t.Fatal(err)
	}
	ts := books[u].Turns
	if len(ts) != 1 {
		t.Fatalf("turns of %s: %+v", u, ts)
	}
	if got := turns.TaskOf(ts[0]); got != "t-"+hexA {
		t.Errorf("the retried turn's task %q", got)
	}
	if got := turns.ItemsOf(ts[0]); !slices.Equal(got, []string{"i1"}) {
		t.Errorf("the retried turn's items %q", got)
	}
}

// notTaskColumns are the columns whose name looks like a task's and that hold none.
var notTaskColumns = map[string]string{
	"pull_requests.owner":        "a GitHub repository's owner",
	"pr_seen.owner":              "a GitHub repository's owner",
	"pr_details.owner":           "a GitHub repository's owner",
	"segment_map.provider":       "a Slack conversation's provider, with no unit key",
	"slack_messages.parent_user": "the author of a Slack thread's parent message",
}

// taskish says whether a column's name suggests it names a task or a unit.
func taskish(name string) bool {
	for _, w := range strings.Split(name, "_") {
		switch w {
		case "task", "tasks", "entry", "provider", "owner", "parent", "project", "alias":
			return true
		}
	}
	return false
}

// refFixture is the current store with, for each of ids, one row in every table that holds a task
// reference, each such column naming that task in its form.
func refFixture(t *testing.T, ids ...string) (*sql.DB, []taskRef) {
	t.Helper()
	path := storeAt(t, current)
	db := rawDB(t, path)
	refs, err := taskRefs(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	reached := map[string]bool{}
	byTable := map[string][]taskRef{}
	for _, r := range refs {
		reached[r.table+"."+r.column] = true
		if r.provider != "" {
			reached[r.table+"."+r.provider] = true
		}
		byTable[r.table] = append(byTable[r.table], r)
	}
	tables := liveTables(t, db)
	for table, cols := range tables {
		for _, c := range cols {
			if taskish(c.name) && !reached[table+"."+c.name] && notTaskColumns[table+"."+c.name] == "" {
				t.Errorf("%s.%s looks like a task reference that rekeyTask does not reach: register it in taskColumns, or in notTaskColumns with why it holds none", table, c.name)
			}
		}
	}
	for i, id := range ids {
		for table, rs := range byTable {
			vals := map[string]string{}
			for _, r := range rs {
				vals[r.column] = r.form.spell(id)
				if r.provider != "" {
					vals[r.provider] = string(unit.Task)
				}
			}
			insertRowAs(t, db, table, tables[table], vals, fmt.Sprint(i))
		}
	}
	return db, refs
}

func inTx(t *testing.T, db *sql.DB, fn func(ctx context.Context, tx *sql.Tx) error) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := fn(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// mentions counts the rows of every table with a column holding s.
func mentions(t *testing.T, db *sql.DB, s string) []string {
	t.Helper()
	var out []string
	for table, cols := range liveTables(t, db) {
		for _, c := range cols {
			if n := strings1(t, db, `SELECT count(*) FROM `+table+` WHERE instr(`+c.name+`, ?) > 0`, s); n[0] != "0" {
				out = append(out, fmt.Sprintf("%s.%s (%s rows)", table, c.name, n[0]))
			}
		}
	}
	slices.Sort(out)
	return out
}

func TestRekeyTaskMovesEveryReference(t *testing.T) {
	t.Parallel()
	from, to := "t-"+hexA, "t-"+hexB
	db, refs := refFixture(t, from)
	if len(refs) == 0 {
		t.Fatal("no task reference found")
	}
	inTx(t, db, func(ctx context.Context, tx *sql.Tx) error { return rekeyTask(ctx, tx, from, to) })
	if left := mentions(t, db, from); len(left) > 0 {
		t.Errorf("task %s is still named in %v", from, left)
	}
	for _, r := range refs {
		q := `SELECT count(*) FROM ` + r.table + ` WHERE ` + r.column + ` = ?`
		args := []any{r.form.spell(to)}
		if r.provider != "" {
			q += ` AND ` + r.provider + ` = ?`
			args = append(args, string(unit.Task))
		}
		if n := strings1(t, db, q, args...); n[0] != "1" {
			t.Errorf("%s.%s does not name %s: %s rows", r.table, r.column, to, n[0])
		}
	}
}

func TestRekeyTaskOntoATaskThatExistsKeepsItsRows(t *testing.T) {
	t.Parallel()
	from, to := "t-"+hexA, "t-"+hexB
	db, refs := refFixture(t, from, to)
	inTx(t, db, func(ctx context.Context, tx *sql.Tx) error { return rekeyTask(ctx, tx, from, to) })
	if left := mentions(t, db, from); len(left) > 0 {
		t.Errorf("task %s is still named in %v", from, left)
	}
	for _, r := range refs {
		if n := strings1(t, db, `SELECT count(*) FROM `+r.table+` WHERE `+r.column+` = ?`, r.form.spell(to)); n[0] == "0" {
			t.Errorf("%s.%s lost task %s", r.table, r.column, to)
		}
	}
	if got := strings1(t, db, `SELECT id FROM tasks`); !slices.Equal(got, []string{to}) {
		t.Errorf("tasks %q, want %s alone", got, to)
	}
}

func TestDropTaskLeavesNoOrphan(t *testing.T) {
	t.Parallel()
	gone := "t-" + hexA
	db, refs := refFixture(t, gone)
	keep := "t-" + hexB
	exec := func(q string, args ...any) {
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO tasks (id, kind, state, origin, created, updated, rev) VALUES (?, 'action', 'next', 'user', 't', 't', 9)`, keep)
	exec(`INSERT INTO messages (task_id, provider, unit_key, kind, text, origin_kind, state, created_at) VALUES (?, 'slack', 'C1/2', 'post', 'p', 'agent', 'open', 't')`, gone)
	inTx(t, db, func(ctx context.Context, tx *sql.Tx) error { return dropTask(ctx, tx, gone) })
	for _, r := range refs {
		if r.form == refText {
			continue
		}
		if n := strings1(t, db, `SELECT count(*) FROM `+r.table+` WHERE `+r.column+` = ?`, r.form.spell(gone)); n[0] != "0" {
			t.Errorf("%s.%s still names the dropped task", r.table, r.column)
		}
	}
	if got := strings1(t, db, `SELECT id FROM tasks`); !slices.Equal(got, []string{keep}) {
		t.Errorf("tasks left %q", got)
	}
	if got := strings1(t, db, `SELECT task_id FROM messages WHERE provider = 'slack'`); !slices.Equal(got, []string{""}) {
		t.Errorf("the Slack message of the dropped task: %q, want it kept without its task", got)
	}
}

func TestV7RenameRewritesTheRenamedWorkflowName(t *testing.T) {
	t.Parallel()
	a := "e-" + hexA
	settled, _ := mark.New(time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC))
	path := v6Store(t,
		`INSERT INTO unit_marks (provider, unit_key, mark, value) VALUES ('entry', '`+a+`', 'entry-results-ended/settled', '`+settled.Value+`')`,
		`INSERT INTO workflow_runs (workflow, provider, unit_key, key_value, status, at) VALUES ('entry-results-ended', 'entry', '`+a+`', 'v', 'done', 't')`,
		`INSERT INTO plan_items (id, workflow, provider, unit_key, key_value, decision, at) VALUES ('entry-results-ended/entry/`+a+`/v', 'entry-results-ended', 'entry', '`+a+`', 'v', 'act', 't')`,
		`INSERT INTO workflow_cursors (workflow, cursor) VALUES ('entry-results-ended', 7)`,
		`INSERT INTO drafts (provider, unit_key, kind, origin_kind, workflow, rule, state, created_at, updated_at) VALUES ('slack', 'C1/1', 'close', 'auto', 'entry-results-ended', 'entry-results-ended', 'proposed', 't', 't')`,
		`INSERT INTO turns (provider, unit_key, intent, turn_id, state, sent_at, workflow) VALUES ('entry', '`+a+`', 'act', 'r1', 'ended', 't', 'entry-results-ended')`,
	)
	db := migrateV6(t, path)
	if left := mentions(t, rawDB(t, path), "entry-results-ended"); len(left) > 0 {
		t.Errorf("the old workflow name is left in %v", left)
	}
	tk := unit.Unit{Provider: unit.Task, Key: "t-" + hexA}
	marks, err := db.Marks(context.Background(), tk)
	if err != nil {
		t.Fatal(err)
	}
	if got := marks[mark.Of("task-results-ended", "settled")]; !mark.Handled(got, time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("marks %v: the settled mark no longer covers what it handled", marks)
	}
	got := strings1(t, rawDB(t, path), `SELECT workflow || ' ' || status FROM workflow_runs WHERE provider = ? AND unit_key = ?`, string(tk.Provider), tk.Key)
	if !slices.Equal(got, []string{"task-results-ended done"}) {
		t.Errorf("runs %q", got)
	}
	if got := strings1(t, rawDB(t, path), `SELECT id FROM plan_items`); !slices.Equal(got, []string{"task-results-ended/task/" + tk.Key + "/v"}) {
		t.Errorf("plan items %q", got)
	}
}
