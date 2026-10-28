package sqlite

import (
	"context"
	"testing"
	"time"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/domain/task"
)

func init() {
	reads = append(reads, "DB.Projects", "projectStore.SlugTaken", "projectStore.CurrentRun")
	writes = append(writes, writeCase{
		method: "projectStore.PutRun",
		call: func(ctx context.Context, e *noteEnv) error {
			return e.db.Projects().PutRun(ctx, task.ReviewRun{ID: "r-1", StartedAt: noteAt})
		},
	})
}

func TestProjectStoreContract(t *testing.T) {
	t.Parallel()
	taskstest.ProjectsContract(t, func(t *testing.T) (coretasks.Store, coretasks.ProjectStore) {
		db, _ := openTemp(t)
		return db.Tasks(), db.Projects()
	})
}

func TestProjectRowsFollowTheirTasks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := openTemp(t)
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	p := task.Task{ID: "e-p", Kind: task.KindProject, State: "next", Category: task.CategoryReady, Title: "P", Slug: "p", ReviewEvery: 36 * time.Hour, Origin: task.OriginUser, Created: at, Updated: at}
	a := task.Task{ID: "e-a", Kind: task.KindCapture, State: "next", Category: task.CategoryReady, Title: "A", Slug: "ignored", Parent: "e-p", Position: 1, Origin: task.OriginUser, Created: at, Updated: at}
	if err := db.Tasks().Apply(ctx, []task.Change{{Op: task.OpCreate, Task: p}, {Op: task.OpCreate, Task: a}}); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := db.R().QueryRowContext(ctx, `SELECT COUNT(*) FROM projects`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("project rows %d, %v; want only the project's", rows, err)
	}
	if taken, _ := db.Projects().SlugTaken(ctx, "ignored"); taken {
		t.Error("an action's slug was stored")
	}
	for i := range 3 {
		p.Title, p.Updated = "P again", at.Add(time.Duration(i+1)*time.Minute)
		if err := db.Tasks().Apply(ctx, []task.Change{{Op: task.OpUpdate, Task: p}}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.Tasks().Task(ctx, "e-p")
	if err != nil || got.Slug != "p" || got.ReviewEvery != 36*time.Hour {
		t.Errorf("after rewrites %+v, %v", got, err)
	}
	q := task.Task{ID: "e-q", Kind: task.KindProject, State: "next", Category: task.CategoryReady, Title: "Q", Slug: "p", Origin: task.OriginUser, Created: at, Updated: at}
	if err := db.Tasks().Apply(ctx, []task.Change{{Op: task.OpCreate, Task: q}}); err == nil {
		t.Error("two projects share a slug")
	}
	r := task.Task{ID: "e-r", Kind: task.KindProject, State: "next", Category: task.CategoryReady, Title: "R", Origin: task.OriginUser, Created: at, Updated: at}
	s := r
	s.ID = "e-s"
	if err := db.Tasks().Apply(ctx, []task.Change{{Op: task.OpCreate, Task: r}, {Op: task.OpCreate, Task: s}}); err != nil {
		t.Errorf("two projects without a slug: %v", err)
	}
}
