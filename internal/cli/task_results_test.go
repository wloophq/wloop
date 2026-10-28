package cli

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/lock"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/placement"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/task"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/source"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// endedFacts are a plugin's facts that say the units it lists have ended.
type endedFacts struct {
	fact  string
	ended map[unit.Unit]bool
}

func (endedFacts) List(context.Context, unit.ProviderID) ([]unit.Unit, error) { return nil, nil }

func (f endedFacts) Facts(_ context.Context, u unit.Unit) (workflow.Facts, error) {
	ended, ok := f.ended[u]
	if !ok {
		return nil, automation.ErrUnitGone
	}
	return workflow.Facts{f.fact: ended}, nil
}

func TestAUnitHasEndedWhenItsPluginsEndedFactSaysSo(t *testing.T) {
	post := slackThreadUnit("T000AAA", "C000AAA", "1.1")
	quiet := slackThreadUnit("T000AAA", "C000AAA", "2.2")
	gone := slackThreadUnit("T000AAA", "C000AAA", "3.3")
	pr := unit.Unit{Provider: unit.GitHub, Key: "acme/api#9"}
	mods := []source.Module{{ID: slackPlugin, Facts: endedFacts{fact: "slack.ended", ended: map[unit.Unit]bool{post: true, quiet: false}}}}
	ended := unitsEnded(mods)
	for u, want := range map[unit.Unit]bool{post: true, quiet: false, gone: false, pr: false, {Provider: "chat", Key: "x"}: false} {
		got, err := ended(context.Background(), u)
		if err != nil || got != want {
			t.Errorf("%s: ended %t, %v; want %t", u, got, err, want)
		}
	}
}

func TestTaskResultsResolveAddressesAndReadTheManifest(t *testing.T) {
	ctx := context.Background()
	store := taskstest.New()
	g := coretasks.New(coretasks.Deps{States: taskstest.BundledStates, Store: store, Clock: clockFunc(func() time.Time { return testNow })})
	if err := store.Apply(ctx, []task.Change{{Op: task.OpCreate, Task: task.Task{ID: "e-1", Kind: task.KindCapture, State: "next", Category: task.CategoryReady, Title: "ship it", Origin: task.OriginUser}}}); err != nil {
		t.Fatal(err)
	}
	reg := dintent.NewRegistry([]dintent.Def{{Name: "begin", AttachPosts: true}, {Name: "reply"}})
	r := taskResults{tasks: g, units: unit.NewRegistry(pr.Codec{}), intents: func() *dintent.Registry { return reg }}
	u, ok := r.UnitOf("https://github.com/acme/api/pull/9")
	if want := (unit.Unit{Provider: unit.GitHub, Key: "acme/api#9"}); !ok || u != want {
		t.Fatalf("UnitOf = %v, %t; want %v", u, ok, want)
	}
	if _, ok := r.UnitOf("https://example.com/notes"); ok {
		t.Fatal("an address of no unit resolves")
	}
	if !r.AttachesPosts("begin") || r.AttachesPosts("reply") || r.AttachesPosts("missing") {
		t.Fatal("AttachesPosts does not read the manifest's drafts.attach_posts")
	}
	if err := r.Attach(ctx, "e-1", u); err != nil {
		t.Fatal(err)
	}
	if e, _ := store.Task(ctx, "e-1"); !slices.Equal(e.Units, []unit.Unit{u}) {
		t.Fatalf("units %v", e.Units)
	}
}

func TestServeWiresTheTaskResults(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.cfg = withoutWindows(te.cfg)
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), storeFile), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	te.serveDeps(context.Background(), db, te.cfg, func(string, ...any) {}, &lock.Mutex{}, nil)
	if te.Turns.Results == nil || te.outbox == nil || te.outbox.Results == nil {
		t.Fatal("the turns' and the Outbox's results are not wired")
	}
}

type fixedOverrides map[unit.Unit]placement.Placement

func (o fixedOverrides) Override(_ context.Context, u unit.Unit) (placement.Placement, bool, error) {
	p, ok := o[u]
	return p, ok, nil
}

func (fixedOverrides) SetOverride(context.Context, unit.Unit, placement.Placement) error { return nil }

func TestATasksRepoIsItsPlacementOverride(t *testing.T) {
	placed, bare := task.UnitOf("t-1"), task.UnitOf("t-2")
	repo := unitsRepo(nil, config.Defaults, fixedOverrides{placed: {Repo: "acme/api"}})
	for u, want := range map[unit.Unit]string{placed: "acme/api", bare: ""} {
		if got, err := repo(context.Background(), u); err != nil || got != want {
			t.Errorf("%s: repo %q, %v; want %q", u, got, err, want)
		}
	}
}
