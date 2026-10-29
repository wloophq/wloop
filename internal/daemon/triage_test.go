package daemon

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

func TestExtractionLagShowsOnTheLens(t *testing.T) {
	ctx := context.Background()
	r := newTasksRig(t, nil)
	r.s.deps.Row = catalog.RowOf(lensFacts(r.db, bundledLoaded, tasksNow))
	behind := unit.Unit{Provider: "slack", Key: "T000AAA/C000AAA/1.1"}
	caught := unit.Unit{Provider: "slack", Key: "T000AAA/C000AAA/2.2"}
	if err := psource.NewSnapshots(r.db.Snapshots()).Put(ctx, map[unit.Unit]src.Snapshot{
		behind: {Rev: 1, At: tasksNow, Extraction: &src.ExtractionHealth{Pending: 3, Oldest: tasksNow.Add(-12 * time.Minute)}},
		caught: {Rev: 1, At: tasksNow},
	}); err != nil {
		t.Fatal(err)
	}
	ask := items.Item{ID: "i1", Unit: behind, Kind: items.Request, Summary: "Can you check the export?", State: items.Open, Basis: items.NoBasis,
		Requester: items.Person{Key: "slack:U0BRUNO", Name: "Bruno"}, WaitingOn: []items.Person{{Key: "github:me"}},
		CreatedAt: tasksNow.Add(-time.Hour), UpdatedAt: tasksNow}
	if err := r.db.Items().Commit(ctx, coreitems.Commit{Items: []items.Item{ask}}); err != nil {
		t.Fatal(err)
	}
	r.put(t, task.Task{ID: "t-behind", State: "next", Category: task.CategoryReady, Title: "Check the export", Units: []unit.Unit{behind},
		Items: []task.ItemRef{{Unit: behind, Item: "i1"}}},
		task.Task{ID: "t-caught", State: "next", Category: task.CategoryReady, Title: "Read the thread", Units: []unit.Unit{caught}})

	byID := map[string]api.TaskView{}
	for _, v := range r.page(t, "view=needs-me").Tasks {
		byID[v.ID] = v
	}
	if got := byID["t-behind"]; got.ExtractionLag != "12m0s" || !slices.Equal(got.Askers, []string{"slack:U0BRUNO"}) || got.OwedBy != "me" {
		t.Errorf("the task behind: lag %q, askers %v, owed by %q", got.ExtractionLag, got.Askers, got.OwedBy)
	}
	if got, ok := byID["t-caught"]; !ok || got.ExtractionLag != "" {
		t.Errorf("the task caught up: %+v, %v; want no lag", got, ok)
	}
	if got := taskIDs(r.page(t, "view=blocked-by-me").Tasks); !slices.Equal(got, []string{"t-behind"}) {
		t.Errorf("blocked by me lists %v", got)
	}
}
