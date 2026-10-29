package sqlite_test

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var propsNow = time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)

func openProps(t *testing.T) (*sqlite.DB, *change.Log) {
	t.Helper()
	log := change.NewLog(change.DefaultWindow, nil)
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "props.db"), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, log
}

func TestPropStoreContract(t *testing.T) {
	taskstest.PropContract(t, func(t *testing.T) (coretasks.PropStore, func(task.ID)) {
		db, _ := openProps(t)
		return db.Props(func() time.Time { return propsNow }), func(id task.ID) {
			applyTasks(t, db.Tasks(), task.Task{ID: id, State: "inbox", Category: task.CategoryInbox, Title: "Call Ana",
				Origin: task.OriginUser, Created: propsNow, Updated: propsNow})
		}
	})
}

func TestSetPropPagesTheTaskAgain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, log := openProps(t)
	applyTasks(t, db.Tasks(), task.Task{ID: "t-01", State: "inbox", Category: task.CategoryInbox, Title: "Call Ana",
		Origin: task.OriginUser, Created: propsNow, Updated: propsNow})
	q := task.Query{View: task.ViewInbox}
	before, err := db.Tasks().TasksSince(ctx, q, coretasks.Since{})
	if err != nil {
		t.Fatal(err)
	}
	from := log.Gen()
	if err := db.Props(func() time.Time { return propsNow }).SetProp(ctx, "t-01", "priority", task.Value{Text: "high"}, task.WriterPerson); err != nil {
		t.Fatal(err)
	}
	if c := log.Since(from, ^port.Topic(0)); c.Topics != port.TopicTasks || !slices.Equal(c.Units, []unit.Unit{task.UnitOf("t-01")}) {
		t.Fatalf("noted %+v", c)
	}
	page, err := db.Tasks().TasksSince(ctx, q, coretasks.Since{Epoch: before.Epoch, Rev: before.Rev})
	if err != nil {
		t.Fatal(err)
	}
	if page.Full || len(page.Tasks) != 1 || page.Tasks[0].ID != "t-01" {
		t.Fatalf("page %+v", page)
	}
}

func TestAnUnchangedSetPropNotesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, log := openProps(t)
	applyTasks(t, db.Tasks(), task.Task{ID: "t-01", State: "inbox", Category: task.CategoryInbox, Title: "Call Ana",
		Origin: task.OriginUser, Created: propsNow, Updated: propsNow})
	props := db.Props(func() time.Time { return propsNow })
	if err := props.SetProp(ctx, "t-01", "priority", task.Value{Text: "high"}, task.WriterPerson); err != nil {
		t.Fatal(err)
	}
	from := log.Gen()
	if err := props.SetProp(ctx, "t-01", "priority", task.Value{Text: "high"}, task.WriterPerson); err != nil {
		t.Fatal(err)
	}
	if err := props.SetProp(ctx, "t-01", "absent", task.Value{}, task.WriterPerson); err != nil {
		t.Fatal(err)
	}
	if c := log.Since(from, ^port.Topic(0)); !c.Empty() {
		t.Fatalf("noted %+v", c)
	}
	if err := props.SetProp(ctx, "t-01", "priority", task.Value{Text: "high"}, task.WriterAgent); err != nil {
		t.Fatal(err)
	}
	if c := log.Since(from, ^port.Topic(0)); c.Topics != port.TopicTasks {
		t.Fatalf("a new writer noted %+v", c)
	}
}
