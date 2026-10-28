package cli

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// profileRig is a test env whose profile's tasks.toml a test rewrites, over a store of its own.
type profileRig struct {
	te  *testEnv
	dir string
	db  *sqlite.DB
	log *change.Log
	n   int
}

func newProfileRig(t *testing.T, tasksTOML string) *profileRig {
	t.Helper()
	r := &profileRig{te: newTestEnv(t, &fake.Manager{}), dir: t.TempDir(), log: change.NewLog(change.DefaultWindow, nil)}
	r.te.cfg.Profile = r.dir
	r.write(t, tasksTOML)
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), r.log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r.db = db
	return r
}

// write replaces the profile's tasks.toml, its time and its directory's moved past any other so the profile
// reloads.
func (r *profileRig) write(t *testing.T, text string) {
	t.Helper()
	p := filepath.Join(r.dir, "tasks.toml")
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	r.n++
	at := time.Date(2036, 1, 1, 0, 0, r.n, 0, time.UTC)
	for _, f := range []string{p, r.dir} {
		if err := os.Chtimes(f, at, at); err != nil {
			t.Fatal(err)
		}
	}
	if ps := r.te.loadedIntents().Problems; len(ps) > 0 {
		t.Fatalf("the profile does not load: %v", ps)
	}
}

func (r *profileRig) put(t *testing.T, id task.ID, state string, c task.Category) {
	t.Helper()
	e := task.Task{ID: id, Kind: task.KindCapture, State: task.State(state), Category: c, Title: string(id), Origin: task.OriginUser,
		Created: testNow, Updated: testNow}
	if err := r.db.Tasks().Apply(context.Background(), []task.Change{{Op: task.OpCreate, Task: e}}); err != nil {
		t.Fatal(err)
	}
}

func (r *profileRig) category(t *testing.T, id task.ID) task.Category {
	t.Helper()
	e, err := r.db.Tasks().Task(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return e.Category
}

func TestAProfileChangingAStatesCategoryRecategorizesTasks(t *testing.T) {
	r := newProfileRig(t, "")
	ctx := context.Background()
	r.put(t, "t-w", "waiting", task.CategoryWaiting)
	r.put(t, "t-n", "next", task.CategoryReady)
	g := r.te.newTasks(r.db, r.te.cfg, nil)
	if unknown, err := g.Recategorize(ctx); err != nil || len(unknown) != 0 || r.category(t, "t-w") != task.CategoryWaiting {
		t.Fatalf("the bundled profile moved a task: %v %v %s", unknown, err, r.category(t, "t-w"))
	}
	r.write(t, "[states.waiting]\ncategory = \"ready\"\n")
	from := r.log.Gen()
	if unknown, err := g.Recategorize(ctx); err != nil || len(unknown) != 0 {
		t.Fatalf("recategorize: %v %v", unknown, err)
	}
	if c := r.category(t, "t-w"); c != task.CategoryReady {
		t.Errorf("a waiting task is %s, want ready", c)
	}
	if c := r.log.Since(from, port.TopicTasks); !slices.Equal(c.Units, []unit.Unit{task.UnitOf("t-w")}) {
		t.Errorf("noted %+v", c)
	}
	got, err := g.Tasks(ctx, task.Query{View: task.ViewNext})
	if err != nil || len(got) != 2 {
		t.Errorf("next lists %d tasks, %v", len(got), err)
	}
}

func TestRemovingAStateTasksHoldIsAProblem(t *testing.T) {
	r := newProfileRig(t, "[states.blocked]\ncategory = \"waiting\"\nlabel = \"Blocked\"\n")
	ctx := context.Background()
	r.put(t, "t-b", "blocked", task.CategoryWaiting)
	g := r.te.newTasks(r.db, r.te.cfg, nil)
	if unknown, err := g.Recategorize(ctx); err != nil || len(unknown) != 0 {
		t.Fatalf("a declared state: %v %v", unknown, err)
	}
	r.write(t, "")
	unknown, err := g.Recategorize(ctx)
	if err != nil || !slices.Equal(unknown, []string{"blocked"}) {
		t.Fatalf("unknown %v, %v", unknown, err)
	}
	if c := r.category(t, "t-b"); c != task.CategoryWaiting {
		t.Errorf("the task of a removed state moved to %s", c)
	}
	problems := r.te.loadedIntents().Tasks.HeldProblems(unknown)
	if len(problems) != 1 || problems[0].File != "tasks.toml" || problems[0].Field != "states.blocked" || !strings.Contains(problems[0].Msg, "blocked") {
		t.Errorf("problems %+v", problems)
	}
}
