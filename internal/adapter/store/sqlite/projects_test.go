package sqlite

import (
	"context"
	"testing"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/domain/task"
)

func init() {
	reads = append(reads, "DB.Projects", "projectStore.CurrentRun")
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
