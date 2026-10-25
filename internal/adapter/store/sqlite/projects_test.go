package sqlite

import (
	"context"
	"testing"
	"time"

	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/core/gtd/gtdtest"
	"github.com/wloophq/wloop/internal/domain/gtd"
)

func init() {
	reads = append(reads, "DB.Projects", "projectStore.SlugTaken", "projectStore.CurrentRun")
	writes = append(writes, writeCase{
		method: "projectStore.PutRun",
		call: func(ctx context.Context, e *noteEnv) error {
			return e.db.Projects().PutRun(ctx, gtd.ReviewRun{ID: "r-1", StartedAt: noteAt})
		},
	})
}

func TestProjectStoreContract(t *testing.T) {
	t.Parallel()
	gtdtest.ProjectsContract(t, func(t *testing.T) (coregtd.Store, coregtd.ProjectStore) {
		db, _ := openTemp(t)
		return db.Entries(), db.Projects()
	})
}

func TestProjectRowsFollowTheirEntries(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := openTemp(t)
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	p := gtd.Entry{ID: "e-p", Kind: gtd.KindProject, State: gtd.Next, Title: "P", Slug: "p", ReviewEvery: 36 * time.Hour, Origin: gtd.OriginUser, Created: at, Updated: at}
	a := gtd.Entry{ID: "e-a", Kind: gtd.KindCapture, State: gtd.Next, Title: "A", Slug: "ignored", Project: "e-p", Position: 1, Origin: gtd.OriginUser, Created: at, Updated: at}
	if err := db.Entries().Apply(ctx, []gtd.Change{{Op: gtd.OpCreate, Entry: p}, {Op: gtd.OpCreate, Entry: a}}); err != nil {
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
		if err := db.Entries().Apply(ctx, []gtd.Change{{Op: gtd.OpUpdate, Entry: p}}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.Entries().Entry(ctx, "e-p")
	if err != nil || got.Slug != "p" || got.ReviewEvery != 36*time.Hour {
		t.Errorf("after rewrites %+v, %v", got, err)
	}
	q := gtd.Entry{ID: "e-q", Kind: gtd.KindProject, State: gtd.Next, Title: "Q", Slug: "p", Origin: gtd.OriginUser, Created: at, Updated: at}
	if err := db.Entries().Apply(ctx, []gtd.Change{{Op: gtd.OpCreate, Entry: q}}); err == nil {
		t.Error("two projects share a slug")
	}
	r := gtd.Entry{ID: "e-r", Kind: gtd.KindProject, State: gtd.Next, Title: "R", Origin: gtd.OriginUser, Created: at, Updated: at}
	s := r
	s.ID = "e-s"
	if err := db.Entries().Apply(ctx, []gtd.Change{{Op: gtd.OpCreate, Entry: r}, {Op: gtd.OpCreate, Entry: s}}); err != nil {
		t.Errorf("two projects without a slug: %v", err)
	}
}
