package sqlite

import (
	"context"
	"database/sql"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/timeline"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

const (
	hexWait     = "00000000000000b1"
	hexAction   = "00000000000000b2"
	hexClose    = "00000000000000b3"
	hexMessage  = "00000000000000b4"
	hexLoose    = "00000000000000b5"
	hexLonely   = "00000000000000b6"
	hexHandWait = "00000000000000b7"
)

var (
	kindsThread = unit.Unit{Provider: "slack", Key: "T1/C1/1.1"}
	kindsPR     = unit.Unit{Provider: "github", Key: "acme/api#9"}

	waitID    = task.ID("t-" + hexWait)
	actionID  = task.ID("t-" + hexAction)
	closeID   = task.ID("t-" + hexClose)
	messageID = task.ID("t-" + hexMessage)
	looseID   = task.ID("t-" + hexLoose)
	lonelyID  = task.ID("t-" + hexLonely)

	v6Nudged    = time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	v6Resurface = time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	v6Snooze    = time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
)

// v6KindsFixture is a version 6 store holding, on a Slack thread: an action e-<action> holding item i9, a
// waiting entry e-<wait> grouping items i2, i1 (the oldest) and i3 waiting on Ana, starred, snoozed and
// nudged, with a turn, a nudge draft, an event, a mark, a session and a dossier; a close entry with its
// closure draft; a message entry whose question is open on a live session with a queued answer. On a pull
// request no task holds, a close entry with its closure draft. A message entry with no unit, and a capture
// someone waits on by hand.
func v6KindsFixture(t *testing.T) string {
	t.Helper()
	w, a, c, m, z, l, h := "e-"+hexWait, "e-"+hexAction, "e-"+hexClose, "e-"+hexMessage, "e-"+hexLoose, "e-"+hexLonely, "e-"+hexHandWait
	waiting := `{"On":[{"key":"slack:U0ANA","name":"Ana"}],"Since":"2026-10-01T09:00:00Z","LastNudge":"` + v6Nudged.Format(time.RFC3339Nano) +
		`","Resurface":"` + v6Resurface.Format(time.RFC3339Nano) + `"}`
	handWaiting := `{"On":[{"key":"slack:U0BRUNO","name":"Bruno"}],"Since":"2026-10-05T09:00:00Z","LastNudge":"0001-01-01T00:00:00Z","Resurface":"0001-01-01T00:00:00Z"}`
	return v6Store(t,
		`INSERT INTO entries (id, kind, state, title, focus_on, focus_auto, focus_at, snooze_until, waiting, origin, created, updated, rev) VALUES
			('`+a+`', 'action', 'next', 'Answer Bruno', 0, '', '', '', '', 'derived', '2026-10-01T08:00:00.000000000Z', '2026-10-01T08:00:00.000000000Z', 1),
			('`+w+`', 'waiting', 'waiting', 'Waiting on Ana', 1, 'stale', '2026-10-02T09:00:00.000000000Z', '`+eventTime(v6Snooze)+`', '`+waiting+`', 'derived', '2026-10-02T09:00:00.000000000Z', '2026-10-21T09:00:00.000000000Z', 2),
			('`+c+`', 'close', 'inbox', 'Close the thread', 0, '', '', '', '', 'derived', '2026-10-03T09:00:00.000000000Z', '2026-10-03T09:00:00.000000000Z', 3),
			('`+m+`', 'message', 'inbox', 'Ship it?', 0, '', '', '', '', 'message', '2026-10-04T09:00:00.000000000Z', '2026-10-04T09:00:00.000000000Z', 4),
			('`+z+`', 'message', 'inbox', 'Remember the notes', 0, '', '', '', '', 'message', '2026-10-04T09:00:00.000000000Z', '2026-10-04T09:00:00.000000000Z', 5),
			('`+l+`', 'close', 'inbox', 'Close the pull request', 0, '', '', '', '', 'derived', '2026-10-04T09:00:00.000000000Z', '2026-10-04T09:00:00.000000000Z', 6),
			('`+h+`', 'capture', 'waiting', 'Get the keys', 0, '', '', '', '`+handWaiting+`', 'user', '2026-10-05T09:00:00.000000000Z', '2026-10-05T09:00:00.000000000Z', 7)`,
		`INSERT INTO entry_units (entry_id, provider, unit_key, ord) VALUES
			('`+a+`', 'slack', 'T1/C1/1.1', 0), ('`+w+`', 'slack', 'T1/C1/1.1', 0), ('`+c+`', 'slack', 'T1/C1/1.1', 0),
			('`+m+`', 'slack', 'T1/C1/1.1', 0), ('`+l+`', 'github', 'acme/api#9', 0)`,
		`INSERT INTO entry_items (entry_id, provider, unit_key, item) VALUES
			('`+a+`', 'slack', 'T1/C1/1.1', 'i9'),
			('`+w+`', 'slack', 'T1/C1/1.1', 'i1'), ('`+w+`', 'slack', 'T1/C1/1.1', 'i2'), ('`+w+`', 'slack', 'T1/C1/1.1', 'i3')`,
		`INSERT INTO items (provider, unit_key, id, body, state, provenance, created_at, updated_at) VALUES
			('slack', 'T1/C1/1.1', 'i1', '{"id":"i1","summary":"Ana sends the logs","initial":"next"}', 'open', 'agent', '2026-10-01T09:00:00.5Z', '2026-10-01T09:00:00.5Z'),
			('slack', 'T1/C1/1.1', 'i2', '{"id":"i2","summary":"Ana reviews the plan","initial":"inbox"}', 'open', 'agent', '2026-10-02T09:00:00Z', '2026-10-02T09:00:00Z'),
			('slack', 'T1/C1/1.1', 'i3', '{"id":"i3","summary":"Ana books the room"}', 'open', 'agent', '2026-10-03T09:00:00Z', '2026-10-03T09:00:00Z'),
			('slack', 'T1/C1/1.1', 'i9', '{"id":"i9","summary":"Answer Bruno"}', 'open', 'agent', '2026-09-30T09:00:00Z', '2026-09-30T09:00:00Z')`,
		`INSERT INTO item_ops (provider, unit_key, item, op, provenance, outcome, at) VALUES
			('slack', 'T1/C1/1.1', 'i1', '{"op":"create","fields":{"summary":"Ana sends the logs","initial":"next"}}', 'agent', 'applied', '2026-10-01T09:00:00Z')`,
		`INSERT INTO item_proposals (id, provider, unit_key, item, op, provenance, dedupe, state, at) VALUES
			(50, 'slack', 'T1/C1/1.1', 'i4', '{"op":"create","fields":{"summary":"Ana signs","initial":"next"}}', 'agent', 'd50', 'open', '2026-10-01T09:00:00Z')`,
		`INSERT INTO turns (seq, provider, unit_key, intent, turn_id, session, state, sent_at, inputs, runner) VALUES
			(1, 'slack', 'T1/C1/1.1', 'act', 'r1', 's1', 'asked', '2026-10-04T09:00:00Z', '{}', ''),
			(2, 'entry', '`+w+`', 'start', 'r2', '', 'ended', '2026-10-04T09:00:00Z', '{}', 'headless'),
			(3, 'entry', '`+c+`', 'start', 'r3', '', 'ended', '2026-10-04T09:00:00Z', '{}', 'headless')`,
		`INSERT INTO sessions (session_id, manager, agent, pane, workspace_id, worktree, started_at, provider, unit_key) VALUES
			('s1', 'orca', 'claude', 'p1', 'ws1', '/work/api-1', '2026-10-04T09:00:00Z', 'slack', 'T1/C1/1.1'),
			('s2', 'orca', 'claude', 'p2', 'ws2', '/work/api-2', '2026-10-04T09:00:00Z', 'entry', '`+w+`')`,
		`INSERT INTO messages (id, entry_id, provider, unit_key, kind, text, origin_kind, turn_seq, session, state, created_at) VALUES
			(10, '`+m+`', 'slack', 'T1/C1/1.1', 'question', 'Ship it?', 'turn', 1, 's1', 'open', '2026-10-04T09:00:00Z'),
			(11, '`+z+`', '', '', 'agent_post', 'Remember the release notes', 'agent', 0, '', 'open', '2026-10-04T09:00:00Z')`,
		`INSERT INTO turn_answers (turn_seq, provider, unit_key, at, text, source, route, session, message_id) VALUES
			(1, 'slack', 'T1/C1/1.1', '2026-10-04T10:00:00Z', 'Yes, ship it', 'cockpit', 'queued', 's1', 10)`,
		`INSERT INTO drafts (id, provider, unit_key, entry, items, kind, origin_kind, workflow, rule, rule_key, state, created_at, updated_at, effects) VALUES
			(60, 'slack', 'T1/C1/1.1', '`+w+`', '["i1","i2","i3"]', 'nudge', 'auto', 'slack-stalled-nudge', 'slack-stalled-nudge', '`+w+`:slack:U0ANA', 'proposed', '2026-10-21T09:00:00Z', '2026-10-21T09:00:00Z', '["entry.nudged"]'),
			(61, 'slack', 'T1/C1/1.1', '`+c+`', '[]', 'close', 'auto', 'slack-closure', 'slack-closure', 'k61', 'proposed', '2026-10-03T09:00:00Z', '2026-10-03T09:00:00Z', '["unit.closed"]'),
			(62, 'github', 'acme/api#9', '`+l+`', '[]', 'close', 'auto', 'github-closure', 'github-closure', 'k62', 'proposed', '2026-10-04T09:00:00Z', '2026-10-04T09:00:00Z', '["unit.closed"]')`,
		`INSERT INTO events (provider, unit_key, kind, key, actor, at, data) VALUES
			('entry', '`+w+`', 'entry.moved', 'm1', 'person', '2026-10-02T09:00:00.000000000Z', 'waiting'),
			('entry', '`+c+`', 'entry.moved', 'm2', 'person', '2026-10-03T09:00:00.000000000Z', 'inbox'),
			('entry', '`+m+`', 'entry.moved', 'm3', 'person', '2026-10-04T09:00:00.000000000Z', 'inbox')`,
		`INSERT INTO unit_marks (provider, unit_key, mark, value) VALUES ('entry', '`+w+`', 'nudge', 'v1'), ('entry', '`+m+`', 'seen', 'v1')`,
		`INSERT INTO dossiers (owner, rel_path, status) VALUES ('entry/`+w+`', 'entry/`+w+`#', 'live')`,
	)
}

func migratedKinds(t *testing.T) (*DB, *sql.DB) {
	t.Helper()
	path := v6KindsFixture(t)
	db := migrateV6(t, path)
	return db, rawDB(t, path)
}

func allGates(t *testing.T, db *DB) []gate.Gate {
	t.Helper()
	all, err := db.Gates(nil).Where(context.Background(), gates.Query{Statuses: []gate.Status{gate.Open, gate.Approved, gate.Rejected,
		gate.Answered, gate.Dismissed, gate.Superseded}})
	if err != nil {
		t.Fatal(err)
	}
	return all
}

// holderOf is the task holding item of the thread.
func holderOf(t *testing.T, raw *sql.DB, item string) task.ID {
	t.Helper()
	got := strings1(t, raw, `SELECT task_id FROM task_items WHERE provider = 'slack' AND unit_key = 'T1/C1/1.1' AND item = ?`, item)
	if len(got) != 1 {
		t.Fatalf("item %s is held by %q, want one task", item, got)
	}
	return task.ID(got[0])
}

func TestV7KindsSplitsAWaitingTaskKeepingItsNudge(t *testing.T) {
	t.Parallel()
	db, raw := migratedKinds(t)
	ids := map[string]task.ID{}
	for _, item := range []string{"i1", "i2", "i3"} {
		ids[item] = holderOf(t, raw, item)
	}
	if ids["i2"] != task.DerivedID(kindsThread, "i2") || ids["i3"] != task.DerivedID(kindsThread, "i3") {
		t.Errorf("split ids %v, want the ids reconcile derives for i2 and i3", ids)
	}
	titles := map[string]string{"i1": "Ana sends the logs", "i2": "Ana reviews the plan", "i3": "Ana books the room"}
	for item, id := range ids {
		e := migratedTask(t, db, string(id)[2:])
		if len(e.Items) != 1 || e.Items[0].Item != item {
			t.Errorf("%s holds %v, want only %s", id, e.Items, item)
		}
		if e.Title != titles[item] || e.Category != task.CategoryWaiting || e.State != "waiting" || e.Origin != task.OriginDerived ||
			!slices.Equal(e.Units, []unit.Unit{kindsThread}) {
			t.Errorf("%s: %+v", id, e)
		}
		if !e.Prop(task.PropLastNudge).Time.Equal(v6Nudged) || e.Written[task.PropLastNudge].By != task.WriterWorkflow {
			t.Errorf("%s last_nudge %v by %s", id, e.Prop(task.PropLastNudge).Time, e.Written[task.PropLastNudge].By)
		}
		if !e.Prop(task.PropCheckOn).Time.Equal(v6Resurface) || e.Written[task.PropCheckOn].By != task.WriterPerson {
			t.Errorf("%s check_on %v by %s", id, e.Prop(task.PropCheckOn).Time, e.Written[task.PropCheckOn].By)
		}
		if !e.Starred() || !e.Snooze.Until.Equal(v6Snooze) {
			t.Errorf("%s star %v snooze %v, want both kept", id, e.Starred(), e.Snooze.Until)
		}
		if e.Prop("autostar").Text != "on" || e.Written[task.PropFocus].By != task.WriterWorkflow {
			t.Errorf("%s autostar %q, focus by %s: want the loop's star kept as one", id, e.Prop("autostar").Text, e.Written[task.PropFocus].By)
		}
		if e.Prop(task.PropKind).Text != "thread" || e.Written[task.PropKind].By != task.WriterPlugin {
			t.Errorf("%s kind %q by %s", id, e.Prop(task.PropKind).Text, e.Written[task.PropKind].By)
		}
		if _, ok := e.Props[task.PropOwner]; ok {
			t.Errorf("%s has an owner; its items say who owes it", id)
		}
	}
	got := strings1(t, raw, `SELECT old_id || ' ' || new_id FROM task_aliases WHERE old_id = ? ORDER BY new_id`, string(waitID))
	want := []string{string(waitID) + " " + string(ids["i2"]), string(waitID) + " " + string(ids["i3"])}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("aliases %q, want %q", got, want)
	}
	hand := migratedTask(t, db, hexHandWait)
	if owner := hand.Prop(task.PropOwner).Actors; len(owner) != 1 || owner[0].Key != "slack:U0BRUNO" || hand.Written[task.PropOwner].By != task.WriterPerson {
		t.Errorf("the hand-typed wait's owner %+v by %s", owner, hand.Written[task.PropOwner].By)
	}
	if _, ok := hand.Props[task.PropCheckOn]; ok {
		t.Error("a wait with no resurface got a check_on")
	}
	if hand.Prop(task.PropKind).Text != "capture" {
		t.Errorf("the hand-typed wait's kind %q", hand.Prop(task.PropKind).Text)
	}
}

func TestV7KindsKeepsTheOldIdOnTheOldestItem(t *testing.T) {
	t.Parallel()
	db, raw := migratedKinds(t)
	if got := holderOf(t, raw, "i1"); got != waitID {
		t.Fatalf("the oldest item is held by %s, want the old id %s", got, waitID)
	}
	w := string(waitID)
	for q, want := range map[string]string{
		`SELECT count(*) FROM turns WHERE provider = 'task' AND unit_key = ?`:                           "1",
		`SELECT count(*) FROM sessions WHERE provider = 'task' AND unit_key = ?`:                        "1",
		`SELECT count(*) FROM events WHERE provider = 'task' AND unit_key = ? AND kind = 'entry.moved'`: "1",
		`SELECT count(*) FROM unit_marks WHERE provider = 'task' AND unit_key = ?`:                      "1",
		`SELECT count(*) FROM drafts WHERE task = ?`:                                                    "1",
		`SELECT count(*) FROM gates WHERE task_id = ? AND subject = '60'`:                               "1",
		`SELECT count(*) FROM dossiers WHERE owner = 'task/' || ?`:                                      "1",
	} {
		if got := strings1(t, raw, q, w); !slices.Equal(got, []string{want}) {
			t.Errorf("%s: %q, want %s", q, got, want)
		}
	}
	for _, item := range []string{"i2", "i3"} {
		id := string(holderOf(t, raw, item))
		if got := mentions(t, raw, id); !slices.Equal(got, []string{"task_aliases.new_id (1 rows)", "task_items.task_id (1 rows)",
			"task_props.task_id (5 rows)", "task_units.task_id (1 rows)", "tasks.id (1 rows)"}) {
			t.Errorf("the split task %s is named by %q, want only its own rows", id, got)
		}
	}
	if e := migratedTask(t, db, hexWait); e.Title != "Ana sends the logs" {
		t.Errorf("the old id's title %q", e.Title)
	}
}

func TestV7KindsMovesMessageGatesToTheUnitsTask(t *testing.T) {
	t.Parallel()
	db, raw := migratedKinds(t)
	all := allGates(t, db)
	if q := gateOf(t, all, gate.KindQuestion, "10"); q.Task != actionID || q.Status != gate.Open {
		t.Errorf("the question's gate %+v, want open on the thread's task %s", q, actionID)
	}
	if got := strings1(t, raw, `SELECT task_id FROM messages WHERE id = 10`); !slices.Equal(got, []string{string(actionID)}) {
		t.Errorf("message 10's task %q", got)
	}
	if got := strings1(t, raw, `SELECT count(*) FROM tasks WHERE id = ?`, string(messageID)); !slices.Equal(got, []string{"0"}) {
		t.Errorf("the message task stays: %q", got)
	}
	if got := strings1(t, raw, `SELECT new_id FROM task_aliases WHERE old_id = ?`, string(messageID)); !slices.Equal(got, []string{string(actionID)}) {
		t.Errorf("the message task's alias %q", got)
	}
	if got := strings1(t, raw, `SELECT count(*) FROM unit_marks WHERE provider = 'task' AND unit_key = ?`, string(actionID)); !slices.Equal(got, []string{"1"}) {
		t.Errorf("the message task's mark on the unit's task: %q", got)
	}
	post := gateOf(t, all, gate.KindPost, "11")
	if post.Task != looseID || post.Status != gate.Open {
		t.Fatalf("the unitless post's gate %+v, want open on %s", post, looseID)
	}
	e := migratedTask(t, db, hexLoose)
	if e.Title != "Remember the release notes" || e.Category != task.CategoryInbox || e.Prop(task.PropKind).Text != "capture" ||
		e.Written[task.PropKind].By != task.WriterWorkflow || e.Origin != task.OriginGate {
		t.Errorf("the unitless message's capture task %+v", e)
	}
}

func TestV7KindsDropsCloseTasksKeepingTheirClosureGates(t *testing.T) {
	t.Parallel()
	db, raw := migratedKinds(t)
	all := allGates(t, db)
	if g := gateOf(t, all, gate.KindDraft, "61"); g.Task != actionID || g.Status != gate.Open {
		t.Errorf("the thread's closure gate %+v, want open on %s", g, actionID)
	}
	if got := strings1(t, raw, `SELECT task FROM drafts WHERE id = 61`); !slices.Equal(got, []string{string(actionID)}) {
		t.Errorf("the closure draft's task %q", got)
	}
	if got := strings1(t, raw, `SELECT count(*) FROM turns WHERE provider = 'task' AND unit_key = ?`, string(actionID)); !slices.Equal(got, []string{"1"}) {
		t.Errorf("the close task's turn on the thread's task: %q", got)
	}
	if got := strings1(t, raw, `SELECT count(*) FROM tasks WHERE id = ?`, string(closeID)); !slices.Equal(got, []string{"0"}) {
		t.Errorf("the close task stays: %q", got)
	}
	if g := gateOf(t, all, gate.KindDraft, "62"); g.Task != lonelyID {
		t.Errorf("the closure gate of a pull request no other task holds %+v, want on %s", g, lonelyID)
	}
	e := migratedTask(t, db, hexLonely)
	if e.Origin != task.OriginGate || e.Category != task.CategoryInbox || e.Prop(task.PropKind).Text != "pull-request" ||
		!slices.Equal(e.Units, []unit.Unit{kindsPR}) {
		t.Errorf("the close task no other task replaces %+v", e)
	}
	a := migratedTask(t, db, hexAction)
	if a.Prop(task.PropKind).Text != "thread" || len(a.Items) != 1 || a.Items[0].Item != "i9" {
		t.Errorf("the thread's task %+v", a)
	}
}

func TestV7KindsKeepsAnAnswerInFlightRoutable(t *testing.T) {
	t.Parallel()
	db, raw := migratedKinds(t)
	ctx := context.Background()
	q := gateOf(t, allGates(t, db), gate.KindQuestion, "10")
	if got := strings1(t, raw, `SELECT gate_id || ' ' || session || ' ' || route FROM turn_answers WHERE message_id = 10`); !slices.Equal(got, []string{itoa(q.ID) + " s1 queued"}) {
		t.Fatalf("the queued answer %q", got)
	}
	var session, closed string
	if err := db.R().QueryRowContext(ctx, `SELECT session, closed_at FROM messages WHERE id = ?`, q.Subject).Scan(&session, &closed); err != nil || session != "s1" || closed != "" {
		t.Fatalf("the gate's subject reads message %s on %q (closed %q): %v", q.Subject, session, closed, err)
	}
	books, err := New(db).Books(ctx, kindsThread)
	if err != nil {
		t.Fatal(err)
	}
	b := books[kindsThread]
	queued := slices.IndexFunc(b.Answers, func(a dturn.Answer) bool { return a.Route == dturn.Queued })
	if len(b.Turns) != 1 || b.Turns[0].Session != "s1" || queued < 0 || b.Answers[queued].Session != "s1" || b.Answers[queued].TurnSeq != 1 {
		t.Fatalf("the turn and answer the question routes through: %+v %+v", b.Turns, b.Answers)
	}
	ts, err := db.Tasks().TasksOf(ctx, kindsThread)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(ts, func(e task.Task) bool { return e.ID == q.Task && e.Open() }) {
		t.Fatalf("the question's task %s is not an open task of the thread: %+v", q.Task, ts)
	}
}

func TestV7KindsLeavesNoOrphanRows(t *testing.T) {
	t.Parallel()
	_, raw := migratedKinds(t)
	for _, gone := range []task.ID{closeID, messageID} {
		if got := mentions(t, raw, string(gone)); !slices.Equal(got, []string{"task_aliases.old_id (1 rows)"}) {
			t.Errorf("the dropped task %s is still named by %q", gone, got)
		}
	}
	for _, id := range []string{"e-" + hexWait, "e-" + hexClose, "e-" + hexMessage} {
		if got := mentions(t, raw, id); len(got) != 0 {
			t.Errorf("the version 6 id %s is named by %q", id, got)
		}
	}
	if got := strings1(t, raw, `SELECT count(*) FROM pragma_foreign_key_check`); !slices.Equal(got, []string{"0"}) {
		t.Errorf("%q foreign key violations", got)
	}
	for _, q := range []string{
		`SELECT task_id AS x FROM task_items UNION SELECT task_id FROM task_units UNION SELECT task_id FROM task_props
			UNION SELECT task_id FROM gates WHERE task_id IS NOT NULL UNION SELECT task_id FROM messages WHERE task_id != ''
			UNION SELECT task FROM drafts WHERE task != '' UNION SELECT new_id FROM task_aliases`,
		`SELECT unit_key AS x FROM turns WHERE provider = 'task' UNION SELECT unit_key FROM events WHERE provider = 'task'
			UNION SELECT unit_key FROM unit_marks WHERE provider = 'task' UNION SELECT unit_key FROM sessions WHERE provider = 'task'`,
	} {
		if got := strings1(t, raw, `SELECT x FROM (`+q+`) WHERE x NOT IN (SELECT id FROM tasks)`); len(got) != 0 {
			t.Errorf("rows name tasks that do not exist: %q", got)
		}
	}
	for _, col := range []string{"waiting", "kind"} {
		if got := strings1(t, raw, `SELECT count(*) FROM pragma_table_info('tasks') WHERE name = ?`, col); !slices.Equal(got, []string{"0"}) {
			t.Errorf("tasks.%s stays", col)
		}
	}
	if got := strings1(t, raw, `SELECT count(*) FROM tasks t WHERE NOT EXISTS (SELECT 1 FROM task_props p WHERE p.task_id = t.id AND p.name = 'kind')`); !slices.Equal(got, []string{"0"}) {
		t.Errorf("%q tasks have no kind", got)
	}
}

func TestV7KindsRewritesTheInitialOfStoredItems(t *testing.T) {
	t.Parallel()
	_, raw := migratedKinds(t)
	for q, want := range map[string][]string{
		`SELECT id || ' ' || coalesce(json_extract(body, '$.initial'), '') FROM items ORDER BY id`: {"i1 ready", "i2 inbox", "i3 ", "i9 "},
		`SELECT json_extract(op, '$.fields.initial') FROM item_ops`:                                {"ready"},
		`SELECT json_extract(op, '$.fields.initial') FROM item_proposals`:                          {"ready"},
	} {
		if got := strings1(t, raw, q); !slices.Equal(got, want) {
			t.Errorf("%s: %q, want %q", q, got, want)
		}
	}
}

func TestV7NormalizesTrimmedEventTimes(t *testing.T) {
	t.Parallel()
	trimmed := "2026-10-23T14:35:10.063897Z"
	path := v6Store(t,
		`INSERT INTO brief_runs (id, trigger, version, attempt_of, started_at, ended_at, outcome, brief, provider, unit_key) VALUES
			(1, 'background', 'b1', 'a', '2026-10-23T14:35:00.5Z', '`+trimmed+`', 'ok', '{"goal":"Export the ledger"}', 'github', 'acme/api#9')`,
		`INSERT INTO unit_briefs (provider, unit_key, run, last_run) VALUES ('github', 'acme/api#9', 1, 1)`,
		`INSERT INTO events (provider, unit_key, kind, key, actor, at, data) VALUES
			('github', 'acme/api#9', 'commented', 'c1', 'ana', '2026-10-23T14:36:00+02:00', ''),
			('github', 'acme/api#9', 'commented', 'c2', 'ana', '2026-10-23T14:37:00Z', ''),
			('github', 'acme/api#9', 'commented', 'c3', 'ana', '2026-10-23T14:38:00.0638-03:00', '')`,
		`INSERT INTO entries (id, kind, state, origin, created, updated, rev) VALUES ('e-`+hexAction+`', 'capture', 'inbox', 'user', '2026-10-23T14:35:10.5Z', '2026-10-23T14:35:10.5Z', 1)`,
	)
	db := migrateV6(t, path)
	raw := rawDB(t, path)
	evs, err := db.EventsOf(context.Background(), []unit.Unit{kindsPR}, timeline.Filter{})
	if err != nil {
		t.Fatalf("reading the pull request's events: %v", err)
	}
	if len(evs) != 4 {
		t.Fatalf("%d events, want the brief record and three comments", len(evs))
	}
	got := strings1(t, raw, `SELECT kind || ' ' || at FROM events ORDER BY at`)
	if !slices.Equal(got, []string{"commented 2026-10-23T12:36:00.000000000Z", "record 2026-10-23T14:35:10.063897000Z", "commented 2026-10-23T14:37:00.000000000Z",
		"commented 2026-10-23T17:38:00.063800000Z"}) {
		t.Errorf("events %q", got)
	}
	if got := strings1(t, raw, `SELECT started_at || ' ' || ended_at FROM brief_runs`); !slices.Equal(got, []string{"2026-10-23T14:35:00.500000000Z 2026-10-23T14:35:10.063897000Z"}) {
		t.Errorf("the brief run's times %q", got)
	}
	if e := migratedTask(t, db, hexAction); !e.Created.Equal(time.Date(2026, 10, 23, 14, 35, 10, 5e8, time.UTC)) {
		t.Errorf("the task's created %v", e.Created)
	}
}

func TestV7KindsGivesAUnitWithNoOpenTaskOneTaskForItsGates(t *testing.T) {
	t.Parallel()
	done, c, m := "e-"+hexAction, "e-"+hexClose, "e-"+hexMessage
	path := v6Store(t,
		`INSERT INTO entries (id, kind, state, title, origin, created, updated, rev) VALUES
			('`+done+`', 'action', 'done', 'Review it', 'derived', '2026-10-01T09:00:00.000000000Z', '2026-10-01T09:00:00.000000000Z', 1),
			('`+c+`', 'close', 'inbox', 'Close it', 'derived', '2026-10-02T09:00:00.000000000Z', '2026-10-02T09:00:00.000000000Z', 2),
			('`+m+`', 'message', 'inbox', 'Merge it?', 'message', '2026-10-03T09:00:00.000000000Z', '2026-10-03T09:00:00.000000000Z', 3)`,
		`INSERT INTO entry_units (entry_id, provider, unit_key, ord) VALUES
			('`+done+`', 'github', 'acme/api#9', 0), ('`+c+`', 'github', 'acme/api#9', 0), ('`+m+`', 'github', 'acme/api#9', 0)`,
		`INSERT INTO messages (id, entry_id, provider, unit_key, kind, text, origin_kind, state, created_at) VALUES
			(20, '`+m+`', 'github', 'acme/api#9', 'question', 'Merge it?', 'turn', 'open', '2026-10-03T09:00:00Z')`,
	)
	db := migrateV6(t, path)
	raw := rawDB(t, path)
	if q := gateOf(t, allGates(t, db), gate.KindQuestion, "20"); q.Task != closeID || q.Status != gate.Open {
		t.Errorf("the question's gate %+v, want open on %s", q, closeID)
	}
	e := migratedTask(t, db, hexClose)
	if e.Origin != task.OriginGate || e.Category != task.CategoryInbox || e.Prop(task.PropKind).Text != "pull-request" {
		t.Errorf("the pull request's open task %+v", e)
	}
	if got := strings1(t, raw, `SELECT old_id || ' ' || new_id FROM task_aliases`); !slices.Equal(got, []string{string(messageID) + " " + string(closeID)}) {
		t.Errorf("aliases %q", got)
	}
	if got := strings1(t, raw, `SELECT id FROM tasks ORDER BY id`); !slices.Equal(got, []string{string(actionID), string(closeID)}) {
		t.Errorf("tasks %q", got)
	}
}

func TestV7KindsMarksOnlyTheLoopsStarsAutostar(t *testing.T) {
	t.Parallel()
	db := migrateV6(t, v6PropsFixture(t))
	if pr := migratedTask(t, db, hexPR); pr.Prop("autostar").Text != "on" || pr.Written["autostar"].By != task.WriterWorkflow {
		t.Errorf("the star AutoFocus set (focus_auto) has autostar %+v by %s", pr.Prop("autostar"), pr.Written["autostar"].By)
	}
	for _, hex := range []string{hexStart, hexCapture} {
		if e := migratedTask(t, db, hex); e.Prop("autostar").Text != "" {
			t.Errorf("%s: a star the person set or cleared got autostar", hex)
		}
	}
}
