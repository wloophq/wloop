package cli

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func TestAPluginRefusedAtBuildCountsAsOff(t *testing.T) {
	t.Parallel()
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	te := newTestEnv(t, &fake.Manager{})
	ctx := context.Background()
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	u := unit.Unit{Provider: "slack", Key: "C1/1.1"}
	e := task.Task{ID: "e-00000000000000ab", Kind: task.KindAction, State: "inbox", Category: task.CategoryInbox, Title: "reply", Units: []unit.Unit{u},
		Origin: task.OriginUser, Created: at, Updated: at}
	if err := db.Tasks().Apply(ctx, []task.Change{{Op: task.OpCreate, Task: e}}); err != nil {
		t.Fatal(err)
	}
	d := daemon.Deps{Plugins: []api.Plugin{{ID: unit.GitHub, Enabled: true}, {ID: "slack"}}}
	d = te.Env.serveTasks(d, db, te.cfg, nil, t.Logf)
	c, err := d.Tasks.Counts(ctx, "")
	if err != nil || c.Views[task.ViewInbox] != 0 || c.Badge != 0 {
		t.Fatalf("counts %+v %v", c, err)
	}
	if _, err := d.Tasks.Do(ctx, e.ID, coretasks.Request{Verb: task.VerbNext}); !errors.Is(err, coretasks.ErrPluginDisabled) {
		t.Fatalf("next on a refused plugin's entry: %v", err)
	}
}
