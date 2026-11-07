package daemon

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestATaskListsItsPendingDrafts(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.put(t, unitTask("t-a", task.OriginDerived, r7u))
	sent := proposed(8, "t-a")
	sent.State = draft.Sent
	failed := proposed(9, "t-a")
	failed.State = draft.Failed
	withOutbox(r, failed, sent, proposed(7, "t-a"), proposed(10, "t-b"))
	var v api.TaskView
	if code := get(t, r.s, api.TaskPath("t-a"), &v); code != 200 {
		t.Fatalf("%d", code)
	}
	if !slices.Equal(v.Drafts, []int64{7, 9}) {
		t.Errorf("detail drafts %v, want [7 9]", v.Drafts)
	}
	page := r.page(t, "view=inbox")
	i := slices.IndexFunc(page.Tasks, func(e api.TaskView) bool { return e.ID == "t-a" })
	if i < 0 || !slices.Equal(page.Tasks[i].Drafts, []int64{7, 9}) {
		t.Errorf("page tasks %+v, want t-a with drafts [7 9]", page.Tasks)
	}
}

func TestATaskDoesNotListTheDraftsOfADisabledPlugin(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, func(d *Deps) { d.PluginEnabled = func(p unit.ProviderID) bool { return p != unit.GitHub } })
	r.put(t, unitTask("t-a", task.OriginDerived, r7u))
	withOutbox(r, proposed(7, "t-a"))
	var v api.TaskView
	if code := get(t, r.s, api.TaskPath("t-a"), &v); code != 200 || len(v.Drafts) != 0 {
		t.Fatalf("%d drafts %v, want none", code, v.Drafts)
	}
}

func TestATaskWithNoDraftsListsNone(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.put(t, unitTask("t-a", task.OriginDerived, r7u))
	var raw map[string]json.RawMessage
	if code := get(t, r.s, api.TaskPath("t-a"), &raw); code != 200 || string(raw["drafts"]) != "[]" {
		t.Fatalf("%d drafts %s, want []", code, raw["drafts"])
	}
}

func TestProposingADraftChangesItsTasksPage(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	r.s.deps.Drafts = r.o
	r.put(t, unitTask("t-a", task.OriginDerived, r7u))
	before := r.page(t, "view=inbox")
	d, err := r.o.Propose(context.Background(), outbox.Proposal{Unit: r7u, Task: "t-a", Kind: draft.Reply, Text: "any news?",
		Origin: draft.Origin{Kind: draft.OriginAuto, Rule: "test", Key: "k"}, Policy: draft.Fixed(draft.Approval{Mode: draft.Wait})})
	if err != nil {
		t.Fatal(err)
	}
	after := r.page(t, "view=inbox")
	if after.Rev == before.Rev {
		t.Errorf("rev %d did not change", after.Rev)
	}
	i := slices.IndexFunc(after.Tasks, func(e api.TaskView) bool { return e.ID == "t-a" })
	if i < 0 || !slices.Equal(after.Tasks[i].Drafts, []int64{int64(d.ID)}) {
		t.Fatalf("t-a after proposing: %+v", after.Tasks)
	}
}

func TestDraftsFilteredByTask(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	r.put(t, unitTask("t-a", task.OriginDerived, r7u), unitTask("t-b", task.OriginDerived, r7u))
	ctx := context.Background()
	propose :=func(taskID, text string) draft.Draft {
		d, err := r.o.Propose(ctx, outbox.Proposal{Unit: r7u, Task: taskID, Kind: draft.Reply, Text: text,
			Origin: draft.Origin{Kind: draft.OriginAuto, Rule: "test", Key: text}, Policy: draft.Fixed(draft.Approval{Mode: draft.Wait})})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	mine, theirs := propose("t-a", "one"), propose("t-b", "two")
	var res api.DraftsResponse
	if code := get(t, r.s, api.Drafts+"?task=t-a&state=held,failed", &res); code != 200 {
		t.Fatalf("GET: %d", code)
	}
	if len(res.Drafts) != 1 || res.Drafts[0].ID != int64(mine.ID) {
		t.Fatalf("drafts %+v, want only %d (not %s)", res.Drafts, mine.ID, strconv.FormatInt(int64(theirs.ID), 10))
	}
}
