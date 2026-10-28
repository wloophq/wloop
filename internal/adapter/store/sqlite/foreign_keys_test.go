package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
)

func countRows(t *testing.T, db *DB, table, where string, args ...any) int {
	t.Helper()
	var n int
	if err := db.R().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM `+table+` WHERE `+where, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func execWrite(db *DB, query string, args ...any) error {
	_, err := db.Write(context.Background(), func(tx *Tx) error {
		_, err := tx.ExecContext(context.Background(), query, args...)
		return err
	})
	return err
}

func projectTask(id task.ID) task.Task {
	e := taskstest.Full(id)
	e.Kind, e.Parent, e.ReviewEvery = task.KindProject, "", 36*time.Hour
	return e
}

func TestDeletingAParentCascades(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := openTemp(t)
	pr := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	if err := db.Worktrees().Keep(ctx, wsdomain.Custody{Path: "/wt/a", Manager: "orca", Units: []unit.Unit{pr}}); err != nil {
		t.Fatal(err)
	}
	if err := db.Tasks().Apply(ctx, []task.Change{{Op: task.OpCreate, Task: projectTask("e-p")}}); err != nil {
		t.Fatal(err)
	}
	children := []struct{ table, column, parent string }{
		{"worktree_units", "path", "/wt/a"},
		{"task_items", "task_id", "e-p"},
		{"task_units", "task_id", "e-p"},
		{"task_props", "task_id", "e-p"},
	}
	for _, c := range children {
		if n := countRows(t, db, c.table, c.column+` = ?`, c.parent); n == 0 {
			t.Fatalf("%s holds no row of %s before the delete", c.table, c.parent)
		}
	}
	if err := execWrite(db, `DELETE FROM worktrees WHERE path = ?`, "/wt/a"); err != nil {
		t.Fatal(err)
	}
	if err := execWrite(db, `DELETE FROM tasks WHERE id = ?`, "e-p"); err != nil {
		t.Fatal(err)
	}
	for _, c := range children {
		if n := countRows(t, db, c.table, c.column+` = ?`, c.parent); n != 0 {
			t.Errorf("%s keeps %d rows of the deleted %s", c.table, n, c.parent)
		}
	}
}

func TestRewritingATaskKeepsItsRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := openTemp(t)
	p := projectTask("e-p")
	if err := db.Tasks().Apply(ctx, []task.Change{{Op: task.OpCreate, Task: p}}); err != nil {
		t.Fatal(err)
	}
	props := countRows(t, db, "task_props", `task_id = 'e-p'`)
	if props == 0 {
		t.Fatal("the task has no property rows")
	}
	p.Kind, p.Title = task.KindCapture, "no longer a project"
	if err := db.Tasks().Apply(ctx, []task.Change{{Op: task.OpUpdate, Task: p}}); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, db, "task_props", `task_id = 'e-p'`); n != props {
		t.Errorf("the property rows of a rewritten task: %d rows, want %d", n, props)
	}
	got, err := db.Tasks().Task(ctx, "e-p")
	if err != nil || got.Title != p.Title || got.Kind != task.KindCapture || len(got.Items) != len(p.Items) || got.ReviewEvery != 36*time.Hour {
		t.Errorf("rewritten task %+v, %v", got, err)
	}
}

func TestAChildWithoutItsParentIsRefused(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	for _, insert := range []string{
		`INSERT INTO worktree_units (path, provider, unit_key, at) VALUES ('/wt/none', 'github', 'acme/api#7', '')`,
		`INSERT INTO task_items (task_id, provider, unit_key, item) VALUES ('e-none', 'github', 'acme/api#7', 'a')`,
		`INSERT INTO task_units (task_id, provider, unit_key, ord) VALUES ('e-none', 'github', 'acme/api#7', 0)`,
		`INSERT INTO task_props (task_id, name, value, writer, at) VALUES ('e-none', 'area', '{"text":"OSS"}', 'person', 't')`,
	} {
		if err := execWrite(db, insert); err == nil || !strings.Contains(err.Error(), "FOREIGN KEY") {
			t.Errorf("%s: %v, want a foreign key error", insert, err)
		}
	}
}
