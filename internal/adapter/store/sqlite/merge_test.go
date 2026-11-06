package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/core/port"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/domain/merge"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/wake"
)

var (
	mergePR     = unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	mergeThread = unit.Unit{Provider: "slack", Key: "T1/C1/1.1"}
	mergeAt     = time.Date(2026, 10, 21, 13, 0, 0, 0, time.UTC)
)

const (
	xID task.ID = "t-00000000000000a1"
	yID task.ID = "t-00000000000000b2"
	zID task.ID = "t-00000000000000c3"
)

func ref(u unit.Unit, item string) task.ItemRef { return task.ItemRef{Unit: u, Item: item} }

type mergeStore struct {
	db *sqlite.DB
	s  coretasks.Store
	m  coretasks.Merger
}

func openMerge(t *testing.T) mergeStore {
	t.Helper()
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "merge.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s := db.Tasks()
	return mergeStore{db: db, s: s, m: s.(coretasks.Merger)}
}

// seed stores X, the pull request's task with a wake, a mark and the rows that act for it, and Y, a Slack ask.
func (ms mergeStore) seed(t *testing.T) (x, y task.Task) {
	t.Helper()
	x = task.Task{ID: xID, State: "next", Title: "Review acme/api#7", Notes: "notes", Items: []task.ItemRef{ref(mergePR, "gh:review")},
		Resolved: []task.ItemRef{ref(mergePR, "gh:ci")}, Units: []unit.Unit{mergePR}, Origin: task.OriginDerived,
		Created: mergeAt.Add(-48 * time.Hour), Updated: mergeAt.Add(-24 * time.Hour), Changed: true, SeenAt: mergeAt.Add(-30 * time.Hour)}
	x.SetProp("area", task.Value{Text: "api"}, task.WriterPerson, mergeAt.Add(-24*time.Hour))
	y = task.Task{ID: yID, State: "inbox", Title: "Finish acme/api#7", Items: []task.ItemRef{ref(mergeThread, "s:ask")},
		Units: []unit.Unit{mergeThread}, Origin: task.OriginDerived, Created: mergeAt.Add(-72 * time.Hour), Updated: mergeAt.Add(-20 * time.Hour)}
	first := x
	first.Updated, first.Title = x.Created, "Review it"
	applyTasks(t, ms.s, first)
	applyTasks(t, ms.s, x, y)
	if err := ms.db.Wakes().Put(context.Background(), wake.Wake{Task: xID, Until: mergeAt.Add(time.Hour), Return: "next"}); err != nil {
		t.Fatal(err)
	}
	sqlitetest.Exec(t, ms.db, `INSERT INTO unit_marks (provider, unit_key, mark, value) VALUES ('task', ?, 'wf-nudge', 'fp-1')`, string(xID))
	gate := func(id int, u unit.Unit, kind, status string) {
		sqlitetest.Exec(t, ms.db, `INSERT INTO gates (id, task_id, provider, unit_key, kind, status, intents, opened_at) VALUES (?, ?, ?, ?, ?, ?, '[]', ?)`,
			id, string(xID), string(u.Provider), u.Key, kind, status, eventTime(mergeAt))
	}
	gate(1, mergePR, "draft", "open")
	gate(2, task.UnitOf(xID), "proposal", "superseded")
	gate(3, mergePR, "question", "approved")
	sqlitetest.Exec(t, ms.db, `INSERT INTO drafts (id, provider, unit_key, task, kind, origin_kind, state, created_at, updated_at) VALUES
		(11, 'github', 'acme/api#7', ?1, 'comment', 'turn', 'held', ?2, ?2), (12, 'github', 'acme/api#7', ?1, 'comment', 'turn', 'sent', ?2, ?2)`,
		string(xID), eventTime(mergeAt))
	sqlitetest.Exec(t, ms.db, `INSERT INTO messages (id, task_id, provider, unit_key, kind, text, origin_kind, created_at) VALUES
		(21, ?1, 'github', 'acme/api#7', 'question', 'Which branch?', 'turn', ?2)`, string(xID), eventTime(mergeAt))
	return ms.read(t, xID), ms.read(t, yID)
}

func eventTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000000Z07:00") }

func (ms mergeStore) read(t *testing.T, id task.ID) task.Task {
	t.Helper()
	e, err := ms.s.Task(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func (ms mergeStore) merge(t *testing.T, from, into task.Task, at time.Time) merge.Record {
	t.Helper()
	y, r, err := merge.Fold(from, into, taskstest.BundledStates, at)
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.m.Merge(context.Background(), r, y); err != nil {
		t.Fatal(err)
	}
	got, ok, err := ms.m.MergeOf(context.Background(), from.ID)
	if err != nil || !ok {
		t.Fatalf("no record of %s: %v", from.ID, err)
	}
	return got
}

func (ms mergeStore) split(t *testing.T, r merge.Record, at time.Time) error {
	t.Helper()
	into := ms.read(t, r.Into)
	from, rest, err := merge.Unfold(into, r, merge.Project{}, taskstest.BundledStates, at)
	if err != nil {
		return err
	}
	return ms.m.Split(context.Background(), r, from, rest)
}

// rows dumps every row a task's state and the rows that act for it hold, but the rev, as text.
func (ms mergeStore) rows(t *testing.T, id task.ID) []string {
	t.Helper()
	var out []string
	for _, q := range [][2]string{
		{"tasks", `SELECT id, state, category, title, notes, parent, position, sequential, changed, seen_at, origin, created, updated, review_every, reviewed_at, open_changed_at FROM tasks WHERE id = ?1`},
		{"task_items", `SELECT provider, unit_key, item, resolved FROM task_items WHERE task_id = ?1 ORDER BY 1, 2, 3`},
		{"task_units", `SELECT provider, unit_key, ord FROM task_units WHERE task_id = ?1 ORDER BY ord`},
		{"task_props", `SELECT name, value, writer, at FROM task_props WHERE task_id = ?1 ORDER BY name`},
		{"wakes", `SELECT until, on_kinds, actors, cond, expires, return_state FROM wakes WHERE task_id = ?1`},
		{"unit_marks", `SELECT mark, value FROM unit_marks WHERE provider = 'task' AND unit_key = ?1 ORDER BY mark`},
		{"gates", `SELECT id, kind, status FROM gates WHERE task_id = ?1 ORDER BY id`},
		{"drafts", `SELECT id, state FROM drafts WHERE task = ?1 ORDER BY id`},
		{"messages", `SELECT id FROM messages WHERE task_id = ?1 ORDER BY id`},
	} {
		rs, err := ms.db.R().QueryContext(context.Background(), q[1], string(id))
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rs.Columns()
		for rs.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rs.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			out = append(out, fmt.Sprintf("%s %v", q[0], vals))
		}
		rs.Close()
	}
	return out
}

func TestMergeThenSplitOfAnUntouchedTaskWritesItBackByteForByte(t *testing.T) {
	t.Parallel()
	ms := openMerge(t)
	x, y := ms.seed(t)
	beforeX, beforeY := ms.rows(t, xID), ms.rows(t, yID)
	r := ms.merge(t, x, y, mergeAt)
	if err := ms.split(t, r, mergeAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := ms.rows(t, xID); !slices.Equal(got, beforeX) {
		t.Fatalf("X after the round trip\n got %q\nwant %q", got, beforeX)
	}
	afterY := ms.rows(t, yID)
	for i := range afterY {
		if strings.HasPrefix(afterY[i], "tasks ") {
			afterY[i], beforeY[i] = "", ""
		}
	}
	if !slices.Equal(afterY, beforeY) {
		t.Fatalf("Y after the round trip\n got %q\nwant %q", afterY, beforeY)
	}
	if got := ms.read(t, yID); !got.Same(y) && !sameButUpdated(got, y) {
		t.Fatalf("Y %+v", got)
	}
	var aliases int
	if err := ms.db.R().QueryRowContext(context.Background(), `SELECT count(*) FROM task_aliases`).Scan(&aliases); err != nil || aliases != 0 {
		t.Fatalf("%d aliases left, %v", aliases, err)
	}
}

func sameButUpdated(a, b task.Task) bool {
	a.Updated = b.Updated
	return a.Same(b)
}

func TestSplitGivesBackTheGatesAndHeldDraftsOfTheMergedTasksUnits(t *testing.T) {
	t.Parallel()
	ms := openMerge(t)
	x, y := ms.seed(t)
	r := ms.merge(t, x, y, mergeAt)
	sqlitetest.Exec(t, ms.db, `INSERT INTO gates (id, task_id, provider, unit_key, kind, status, intents, opened_at) VALUES
		(4, ?1, 'github', 'acme/api#7', 'draft', 'open', '[]', ?2), (5, ?1, 'slack', 'T1/C1/1.1', 'draft', 'open', '[]', ?2)`, string(yID), eventTime(mergeAt))
	sqlitetest.Exec(t, ms.db, `INSERT INTO drafts (id, provider, unit_key, task, kind, origin_kind, state, created_at, updated_at) VALUES
		(13, 'github', 'acme/api#7', ?1, 'comment', 'turn', 'held', ?2, ?2), (14, 'github', 'acme/api#7', ?1, 'comment', 'turn', 'sent', ?2, ?2),
		(15, 'slack', 'T1/C1/1.1', ?1, 'reply', 'turn', 'held', ?2, ?2)`, string(yID), eventTime(mergeAt))
	if err := ms.split(t, r, mergeAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	want := map[task.ID][]string{
		xID: {"gates [1 draft open]", "gates [2 proposal superseded]", "gates [3 question approved]", "gates [4 draft open]",
			"drafts [11 held]", "drafts [12 sent]", "drafts [13 held]", "messages [21]"},
		yID: {"gates [5 draft open]", "drafts [14 sent]", "drafts [15 held]"},
	}
	for id, w := range want {
		got := slices.DeleteFunc(ms.rows(t, id), func(s string) bool {
			return !strings.HasPrefix(s, "gates ") && !strings.HasPrefix(s, "drafts ") && !strings.HasPrefix(s, "messages ")
		})
		if !slices.Equal(got, w) {
			t.Errorf("%s holds\n %q\nwant %q", id, got, w)
		}
	}
}

func TestMergeMovesTheGatesDraftsAndMessagesOfTheMergedTask(t *testing.T) {
	t.Parallel()
	ms := openMerge(t)
	x, y := ms.seed(t)
	r := ms.merge(t, x, y, mergeAt)
	if !slices.Equal(r.Gates, []int64{1, 2, 3}) || !slices.Equal(r.Drafts, []int64{11, 12}) || !slices.Equal(r.Messages, []int64{21}) {
		t.Fatalf("record %+v", r)
	}
	if r.Wake == nil || r.Wake.Return != "next" || !reflect.DeepEqual(r.Marks, map[string]string{"wf-nudge": "fp-1"}) {
		t.Fatalf("wake %+v, marks %v", r.Wake, r.Marks)
	}
	if got := ms.rows(t, xID); len(got) != 1 || !strings.HasPrefix(got[0], "unit_marks") {
		t.Fatalf("X's rows after the merge: %q", got)
	}
	got := ms.rows(t, yID)
	for _, w := range []string{"gates [1 draft open]", "gates [2 proposal superseded]", "gates [3 question approved]", "drafts [11 held]", "drafts [12 sent]", "messages [21]"} {
		if !slices.Contains(got, w) {
			t.Errorf("Y lacks %s: %q", w, got)
		}
	}
	if !r.Task.Same(x) {
		t.Fatalf("the record's task\n got %+v\nwant %+v", r.Task, x)
	}
}

func TestMergeHandlesEveryColumnTaskRefsFinds(t *testing.T) {
	t.Parallel()
	ms := openMerge(t)
	missing, err := sqlite.UnhandledMergeColumns(ms.db)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) > 0 {
		t.Fatalf("a merge does not say how it treats %v: add each to mergeColumns", missing)
	}
	if len(sqlite.TaskColumns()) == 0 {
		t.Fatal("no task columns")
	}
}

func TestAMergedTasksIDReadsAsTheSurvivor(t *testing.T) {
	t.Parallel()
	ms := openMerge(t)
	x, y := ms.seed(t)
	ms.merge(t, x, y, mergeAt)
	if got := ms.read(t, xID); got.ID != yID || !slices.Contains(got.Items, ref(mergePR, "gh:review")) {
		t.Fatalf("X reads as %+v", got)
	}
	open, err := ms.s.Tasks(context.Background(), task.Query{All: true})
	if err != nil || len(open) != 1 || open[0].ID != yID {
		t.Fatalf("tasks %v, %v", open, err)
	}
	of, err := ms.s.TasksOf(context.Background(), mergePR)
	if err != nil || len(of) != 1 || of[0].ID != yID {
		t.Fatalf("the pull request's tasks %v, %v", of, err)
	}
}

func TestSplitKeepsTheMarksRetentionDroppedWhileMerged(t *testing.T) {
	t.Parallel()
	ms := openMerge(t)
	x, y := ms.seed(t)
	r := ms.merge(t, x, y, mergeAt)
	if _, err := ms.db.Retain(context.Background(), mergeAt.Add(time.Hour), sqlite.DefaultRetention); err != nil {
		t.Fatal(err)
	}
	if got := ms.rows(t, xID); len(got) != 0 {
		t.Fatalf("retention kept %q", got)
	}
	if err := ms.split(t, r, mergeAt.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := ms.rows(t, xID); !slices.Contains(got, "unit_marks [wf-nudge fp-1]") {
		t.Fatalf("X's marks after the split: %q", got)
	}
}

func TestSplitRefusesATaskStoredAgain(t *testing.T) {
	t.Parallel()
	ms := openMerge(t)
	x, y := ms.seed(t)
	r := ms.merge(t, x, y, mergeAt)
	if err := ms.s.Apply(context.Background(), []task.Change{{Op: task.OpCreate, Task: capture(xID, "inbox", 1)}}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := ms.m.MergeOf(context.Background(), xID); ok || err != nil {
		t.Fatalf("a stored task has a merge record: %v", err)
	}
	from, rest, err := merge.Unfold(ms.read(t, yID), r, merge.Project{}, taskstest.BundledStates, mergeAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.m.Split(context.Background(), r, from, rest); !errors.Is(err, coretasks.ErrVerb) {
		t.Fatalf("split of a task stored again: %v", err)
	}
}

func TestSplitRefusesAPairTheRecordDoesNotName(t *testing.T) {
	t.Parallel()
	ms := openMerge(t)
	x, y := ms.seed(t)
	r := ms.merge(t, x, y, mergeAt)
	from, rest, err := merge.Unfold(ms.read(t, yID), r, merge.Project{}, taskstest.BundledStates, mergeAt)
	if err != nil {
		t.Fatal(err)
	}
	other := from
	other.ID = zID
	if err := ms.m.Split(context.Background(), r, other, rest); !errors.Is(err, coretasks.ErrVerb) {
		t.Fatalf("split writing another task back: %v", err)
	}
	wrong := rest
	wrong.ID = zID
	if err := ms.m.Split(context.Background(), r, from, wrong); !errors.Is(err, coretasks.ErrVerb) {
		t.Fatalf("split writing another survivor: %v", err)
	}
	if got := ms.read(t, xID); got.ID != yID {
		t.Fatalf("a refused split wrote %+v", got)
	}
}

func TestTaskFollowsAnAliasChainToTheStoredTask(t *testing.T) {
	t.Parallel()
	ms := openMerge(t)
	x, y := ms.seed(t)
	ms.merge(t, x, y, mergeAt)
	z := capture(zID, "next", 1)
	applyTasks(t, ms.s, z)
	ry := ms.merge(t, ms.read(t, yID), ms.read(t, zID), mergeAt.Add(time.Hour))
	for _, id := range []task.ID{xID, yID, zID} {
		if got := ms.read(t, id); got.ID != zID {
			t.Fatalf("%s reads as %s", id, got.ID)
		}
	}
	rx, _, _ := ms.m.MergeOf(context.Background(), xID)
	if err := ms.split(t, rx, mergeAt.Add(2*time.Hour)); err == nil {
		t.Fatal("an inner merge split before the outer one")
	}
	if err := ms.split(t, ry, mergeAt.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := ms.read(t, xID); got.ID != yID {
		t.Fatalf("after the outer split, X reads as %s", got.ID)
	}
	if err := ms.split(t, rx, mergeAt.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	for _, id := range []task.ID{xID, yID, zID} {
		if got := ms.read(t, id); got.ID != id {
			t.Fatalf("%s reads as %s", id, got.ID)
		}
	}
}

func TestTaskRefusesAnAliasCycle(t *testing.T) {
	t.Parallel()
	ms := openMerge(t)
	sqlitetest.Exec(t, ms.db, `INSERT INTO task_aliases (old_id, new_id) VALUES (?1, ?2), (?2, ?1)`, string(xID), string(yID))
	if _, err := ms.s.Task(context.Background(), xID); !errors.Is(err, coretasks.ErrNoTask) {
		t.Fatalf("a cycle: %v", err)
	}
}

func TestMergeOfReadsTheRecordOfTheHighestEventID(t *testing.T) {
	t.Parallel()
	ms := openMerge(t)
	x, y := ms.seed(t)
	first := ms.merge(t, x, y, mergeAt)
	if err := ms.split(t, first, mergeAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	applyTasks(t, ms.s, capture(zID, "next", 1))
	second := ms.merge(t, ms.read(t, xID), ms.read(t, zID), mergeAt.Add(-time.Hour))
	got, ok, err := ms.m.MergeOf(context.Background(), xID)
	if err != nil || !ok || got.Into != zID || second.Into != zID {
		t.Fatalf("latest record %+v, %v", got, err)
	}
}

func TestApplyRefusesAnUpdateOfAMergedTask(t *testing.T) {
	t.Parallel()
	ms := openMerge(t)
	x, y := ms.seed(t)
	ms.merge(t, x, y, mergeAt)
	stale := x
	stale.Title = "a sync that read X before the merge"
	err := ms.s.Apply(context.Background(), []task.Change{{Op: task.OpUpdate, Task: stale}})
	if !errors.Is(err, coretasks.ErrNoTask) {
		t.Fatalf("a stale update: %v", err)
	}
	if got := ms.read(t, xID); got.ID != yID {
		t.Fatalf("X came back: %+v", got)
	}
	created := capture("t-00000000000000d4", "inbox", 1)
	if err := ms.s.Apply(context.Background(), []task.Change{{Op: task.OpUpdate, Task: created}}); err != nil {
		t.Fatalf("an update of an id no alias names: %v", err)
	}
}

func TestMergeAndSplitStartANewPositionEpoch(t *testing.T) {
	t.Parallel()
	ms := openMerge(t)
	x, y := ms.seed(t)
	before, err := ms.s.TasksSince(context.Background(), task.Query{}, coretasks.Since{})
	if err != nil {
		t.Fatal(err)
	}
	r := ms.merge(t, x, y, mergeAt)
	page, err := ms.s.TasksSince(context.Background(), task.Query{}, coretasks.Since{Epoch: before.Epoch, Rev: before.Rev})
	if err != nil || !page.Full || slices.ContainsFunc(page.Tasks, func(e task.Task) bool { return e.ID == xID }) {
		t.Fatalf("after the merge: full %v, %d tasks, %v", page.Full, len(page.Tasks), err)
	}
	if err := ms.split(t, r, mergeAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	again, err := ms.s.TasksSince(context.Background(), task.Query{}, coretasks.Since{Epoch: page.Epoch, Rev: page.Rev})
	if err != nil || !again.Full || again.Epoch == page.Epoch {
		t.Fatalf("after the split: full %v, %v", again.Full, err)
	}
}

var _ = port.Event{}
