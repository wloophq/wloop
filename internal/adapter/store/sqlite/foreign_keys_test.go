package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/gtd/gtdtest"
	"github.com/wloophq/wloop/internal/domain/gtd"
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

func projectEntry(id gtd.ID) gtd.Entry {
	e := gtdtest.Full(id)
	e.Kind, e.Project, e.Slug, e.ReviewEvery = gtd.KindProject, "", "p", 36*time.Hour
	return e
}

func TestDeletingAParentCascades(t *testing.T) {
	ctx := context.Background()
	db, _ := openTemp(t)
	pr := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	if err := db.Worktrees().Keep(ctx, wsdomain.Custody{Path: "/wt/a", Manager: "orca", Units: []unit.Unit{pr}}); err != nil {
		t.Fatal(err)
	}
	if err := db.Entries().Apply(ctx, []gtd.Change{{Op: gtd.OpCreate, Entry: projectEntry("e-p")}}); err != nil {
		t.Fatal(err)
	}
	children := []struct{ table, column, parent string }{
		{"worktree_units", "path", "/wt/a"},
		{"entry_items", "entry_id", "e-p"},
		{"entry_units", "entry_id", "e-p"},
		{"entry_contexts", "entry_id", "e-p"},
		{"projects", "entry_id", "e-p"},
	}
	for _, c := range children {
		if n := countRows(t, db, c.table, c.column+` = ?`, c.parent); n == 0 {
			t.Fatalf("%s holds no row of %s before the delete", c.table, c.parent)
		}
	}
	if err := execWrite(db, `DELETE FROM worktrees WHERE path = ?`, "/wt/a"); err != nil {
		t.Fatal(err)
	}
	if err := execWrite(db, `DELETE FROM entries WHERE id = ?`, "e-p"); err != nil {
		t.Fatal(err)
	}
	for _, c := range children {
		if n := countRows(t, db, c.table, c.column+` = ?`, c.parent); n != 0 {
			t.Errorf("%s keeps %d rows of the deleted %s", c.table, n, c.parent)
		}
	}
}

func TestRewritingAnEntryKeepsItsProjectRow(t *testing.T) {
	ctx := context.Background()
	db, _ := openTemp(t)
	p := projectEntry("e-p")
	if err := db.Entries().Apply(ctx, []gtd.Change{{Op: gtd.OpCreate, Entry: p}}); err != nil {
		t.Fatal(err)
	}
	p.Kind, p.Title = gtd.KindCapture, "no longer a project"
	if err := db.Entries().Apply(ctx, []gtd.Change{{Op: gtd.OpUpdate, Entry: p}}); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, db, "projects", `entry_id = 'e-p' AND slug = 'p' AND review_every = ?`, int64(36*time.Hour/time.Second)); n != 1 {
		t.Errorf("the project row of a rewritten entry: %d rows, want 1", n)
	}
	got, err := db.Entries().Entry(ctx, "e-p")
	if err != nil || got.Title != p.Title || got.Kind != gtd.KindCapture || len(got.Items) != len(p.Items) {
		t.Errorf("rewritten entry %+v, %v", got, err)
	}
}

func TestAChildWithoutItsParentIsRefused(t *testing.T) {
	db, _ := openTemp(t)
	for _, insert := range []string{
		`INSERT INTO worktree_units (path, provider, unit_key, at) VALUES ('/wt/none', 'github', 'acme/api#7', '')`,
		`INSERT INTO entry_items (entry_id, provider, unit_key, item) VALUES ('e-none', 'github', 'acme/api#7', 'a')`,
		`INSERT INTO entry_units (entry_id, provider, unit_key, ord) VALUES ('e-none', 'github', 'acme/api#7', 0)`,
		`INSERT INTO entry_contexts (entry_id, context, ord) VALUES ('e-none', '@reply', 0)`,
		`INSERT INTO projects (entry_id) VALUES ('e-none')`,
	} {
		if err := execWrite(db, insert); err == nil || !strings.Contains(err.Error(), "FOREIGN KEY") {
			t.Errorf("%s: %v, want a foreign key error", insert, err)
		}
	}
}
