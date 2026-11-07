package cli

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/source/github/ghsourcetest"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/core/timeline"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/link"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func TestALinkTheLedgerStoresOnAHeldItemRecordsTaskLinked(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	cfg := Defaults()
	logf := func(string, ...any) {}
	te.serveTasks(daemon.Deps{}, db, cfg, nil, logf)
	from := unit.Unit{Provider: unit.GitHub, Key: "acme/api#3"}
	pr := unit.Unit{Provider: unit.GitHub, Key: "acme/api#4"}
	now := te.Now()
	holder := task.Task{ID: "t-holder", State: "next", Category: task.CategoryReady, Origin: task.OriginUser, Title: "Ship the fix",
		Units: []unit.Unit{from}, Items: []task.ItemRef{{Unit: from, Item: "i1"}}, Created: now, Updated: now}
	if err := db.Tasks().Apply(ctx, []task.Change{{Op: task.OpCreate, Task: holder}}); err != nil {
		t.Fatal(err)
	}
	at := unit.Anchor{Unit: from, Ref: "c:1"}
	kind := items.Request
	summary := "review the fix"
	res, err := te.itemsLedger(db, cfg, logf).Apply(ctx, items.Extracted, []items.Op{
		{Op: items.OpCreate, Unit: from, Item: "i1", Anchor: at, Fields: &items.Fields{Kind: &kind, Summary: &summary}},
		{Op: items.OpLink, Unit: from, Item: "i1", Anchor: at, To: &pr, LinkKind: link.Review},
	})
	if err != nil || len(res.Refused) > 0 {
		t.Fatalf("%+v %v", res, err)
	}
	evs, err := db.EventsOf(ctx, []unit.Unit{task.UnitOf(holder.ID)}, timeline.Filter{Kinds: []string{task.EventLinked}})
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].Data != pr.String() {
		t.Fatalf("events %+v, want the link's task_linked", evs)
	}
}

func TestEveryTaskASyncDerivesOnARealStoreRecordsTaskCreatedOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cases := ghsourcetest.Cases()
	r := newDerivedRig(t, cases)
	var units []unit.Unit
	for _, c := range cases {
		units = append(units, c.Row.Unit)
	}
	if err := r.svc.Sync(ctx, units); err != nil {
		t.Fatal(err)
	}
	es, err := r.db.Tasks().Tasks(ctx, task.Query{All: true})
	if err != nil {
		t.Fatal(err)
	}
	derived := 0
	for _, e := range es {
		evs, err := r.db.EventsOf(ctx, []unit.Unit{task.UnitOf(e.ID)}, timeline.Filter{Kinds: []string{task.EventCreated}})
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		if e.Origin == task.OriginDerived {
			want, derived = 1, derived+1
		}
		if len(evs) != want {
			t.Errorf("%s (%s) has %d task_created events, want %d", e.ID, e.Origin, len(evs), want)
		}
	}
	if derived == 0 {
		t.Fatal("no derived task")
	}
}
