package sqlite_test

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/dossier"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// userStates are a profile's states whose names say nothing of their categories.
var userStates = map[string]task.Category{"next": task.CategoryInbox, "blocked": task.CategoryWaiting, "doing": task.CategoryActive,
	"archived": task.CategoryDone, "trash": task.CategoryReady}

func inUserState(id task.ID, state string, units ...unit.Unit) task.Task {
	return task.Task{ID: id, State: task.State(state), Category: userStates[state], Title: string(id),
		Units: units, Origin: task.OriginUser, Created: taskAt, Updated: taskAt}
}

func TestStoreFiltersByCategory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log := change.NewLog(change.DefaultWindow, nil)
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "tasks.db"), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s := db.Tasks()
	thread := unit.Unit{Provider: "slack", Key: "T1/C1/1.1"}
	all := []task.Task{inUserState("t-next", "next"), inUserState("t-blocked", "blocked"), inUserState("t-doing", "doing"),
		inUserState("t-archived", "archived", thread), inUserState("t-trash", "trash")}
	var cs []task.Change
	for _, e := range all {
		cs = append(cs, task.Change{Op: task.OpCreate, Task: e})
	}
	if err := s.Apply(ctx, cs); err != nil {
		t.Fatal(err)
	}
	for v, want := range map[task.View][]task.ID{
		task.ViewInbox: {"t-next"}, task.ViewWaiting: {"t-blocked"}, task.ViewNext: {"t-trash"}, task.ViewLogbook: {"t-archived"},
	} {
		got, err := s.Tasks(ctx, task.Query{View: v})
		if err != nil {
			t.Fatal(err)
		}
		if ids := taskIDs(got); !slices.Equal(ids, want) {
			t.Errorf("%s lists %v, want %v", v, ids, want)
		}
	}
	c, err := s.Counts(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Views[task.ViewInbox] != 1 || c.Views[task.ViewNext] != 1 || c.Views[task.ViewWaiting] != 1 || c.Views[task.ViewLogbook] != 1 {
		t.Errorf("counters %+v", c.Views)
	}
	if open, err := db.Dossiers().Open(ctx, dossier.Owner{Unit: thread}); err != nil || open {
		t.Errorf("a task in a done-category state keeps its unit open: %v %v", open, err)
	}

	from := log.Gen()
	unknown, err := s.(coretasks.Recategorizer).Recategorize(ctx, func(state string) (task.Category, bool) {
		if state == "doing" {
			return "", false
		}
		if state == "blocked" {
			return task.CategoryReady, true
		}
		c, ok := userStates[state]
		return c, ok
	})
	if err != nil || !slices.Equal(unknown, []string{"doing"}) {
		t.Fatalf("unknown %v, %v", unknown, err)
	}
	got, err := s.Tasks(ctx, task.Query{View: task.ViewNext})
	if err != nil || !slices.Equal(taskIDs(got), []task.ID{"t-blocked", "t-trash"}) {
		t.Fatalf("after recategorizing, next lists %v, %v", taskIDs(got), err)
	}
	if doing, err := s.Task(ctx, "t-doing"); err != nil || doing.Category != task.CategoryActive {
		t.Errorf("a task of an unknown state changed category: %+v %v", doing, err)
	}
	if ch := log.Since(from, port.TopicTasks); !slices.Equal(ch.Units, []unit.Unit{task.UnitOf("t-blocked")}) {
		t.Errorf("the recategorize noted %+v", ch)
	}
}
