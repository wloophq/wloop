package daemon

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestADisabledPluginsTasksAndDraftsLeaveTheCountsAndStayDismissable(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t, unit.GitHub, "slack")
	ctx := context.Background()
	r.put(t, task.Task{ID: "e-gh", Kind: task.KindAction, State: "inbox", Category: task.CategoryInbox, Title: "reply", Units: []unit.Unit{r7u}})
	r.put(t, task.Task{ID: "e-mine", Kind: task.KindAction, State: "inbox", Category: task.CategoryInbox, Title: "mine"})
	d := r.stored(t, draft.Draft{Unit: slackUnit, Text: "hi", State: draft.Proposed})

	inbox := r.page(t, "view=inbox")
	if got := taskIDs(inbox.Tasks); len(got) != 1 || got[0] != "e-mine" {
		t.Fatalf("inbox %v", got)
	}
	if c := inbox.Counters; c.Badge != 1 || c.Views["inbox"] != 1 || c.Views["outbox"] != 0 {
		t.Fatalf("counters %+v", c)
	}
	if p := r.page(t, "view=outbox"); len(p.Tasks) != 0 {
		t.Fatalf("outbox %v", taskIDs(p.Tasks))
	}
	var v api.TaskView
	if code := get(t, r.s, api.TaskPath("e-gh"), &v); code != 200 {
		t.Fatalf("GET by id: %d", code)
	}
	for _, vs := range v.Verbs {
		if vs.Enabled != (vs.Verb == "dismiss") {
			t.Errorf("%+v", vs)
		}
	}
	if rec := post(t, r.s, api.TaskPath("e-gh", "next"), `{"source":"cockpit"}`); rec.Code != 409 {
		t.Fatalf("next: %d %s", rec.Code, rec.Body)
	}
	if rec := post(t, r.s, api.TaskPath("e-gh", "dismiss"), `{"source":"cockpit"}`); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"state":"trash"`) {
		t.Fatalf("dismiss: %d %s", rec.Code, rec.Body)
	}
	var dv api.TaskView
	if code := get(t, r.s, api.TaskPath(fmt.Sprintf("draft:%d", d.ID)), &dv); code != 200 {
		t.Fatalf("GET the draft: %d", code)
	}
	for _, vs := range dv.Verbs {
		if vs.Enabled != (vs.Verb == "dismiss") {
			t.Errorf("draft %+v", vs)
		}
	}
	if code, body := r.verb(t, d.ID, "approve", `{"rev":"0"}`); code != 409 {
		t.Fatalf("approve: %d %s", code, body)
	}
	if code, body := r.verb(t, d.ID, "dismiss", `{}`); code != 200 {
		t.Fatalf("dismiss the draft: %d %s", code, body)
	}
	if got, _ := r.o.Store.Get(ctx, d.ID); got.State != draft.Discarded {
		t.Fatalf("draft %+v", got)
	}
}

func TestDismissDetachesADisabledPluginFromAMixedTask(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t, "slack")
	r.put(t, task.Task{ID: "e-mixed", Kind: task.KindAction, State: "inbox", Category: task.CategoryInbox, Title: "both", Units: []unit.Unit{slackUnit, r7u}})
	if got := taskIDs(r.page(t, "view=inbox").Tasks); len(got) != 1 || got[0] != "e-mixed" {
		t.Fatalf("inbox %v", got)
	}
	var v api.TaskView
	if code := get(t, r.s, api.TaskPath("e-mixed"), &v); code != 200 {
		t.Fatalf("GET: %d", code)
	}
	for _, vs := range v.Verbs {
		if vs.Enabled != (vs.Verb == "dismiss") || !vs.Enabled && vs.Reason != "slack is disabled" {
			t.Errorf("%+v", vs)
		}
	}
	if rec := post(t, r.s, api.TaskPath("e-mixed", "next"), `{"source":"cockpit"}`); rec.Code != 409 || !strings.Contains(rec.Body.String(), `"plugin":"slack"`) {
		t.Fatalf("next: %d %s", rec.Code, rec.Body)
	}
	rec := post(t, r.s, api.TaskPath("e-mixed", "dismiss"), `{"source":"cockpit"}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"state":"inbox"`) {
		t.Fatalf("dismiss: %d %s", rec.Code, rec.Body)
	}
	if got, err := r.g.Task(context.Background(), "e-mixed"); err != nil || !slices.Equal(got.Units, []unit.Unit{r7u}) || got.State != task.State("inbox") {
		t.Fatalf("detached %+v %v", got, err)
	}
	var after api.TaskView
	if code := get(t, r.s, api.TaskPath("e-mixed"), &after); code != 200 {
		t.Fatalf("GET: %d", code)
	}
	for _, vs := range after.Verbs {
		if vs.Reason == "slack is disabled" {
			t.Errorf("still refused: %+v", vs)
		}
	}
	r.put(t, task.Task{ID: "e-trashed", Kind: task.KindAction, State: "trash", Category: task.CategoryDropped, Title: "gone", Units: []unit.Unit{r7u, slackUnit}})
	var trashed api.TaskView
	if code := get(t, r.s, api.TaskPath("e-trashed"), &trashed); code != 200 {
		t.Fatalf("GET: %d", code)
	}
	for _, vs := range trashed.Verbs {
		if vs.Verb == "dismiss" && (vs.Enabled || vs.Reason == "") {
			t.Errorf("dismiss on a trashed entry: %+v", vs)
		}
	}
}

func TestDismissOnAMixedTaskRunsItsEnabledUnitsAction(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t, "slack")
	r.put(t, task.Task{ID: "e-mixed", Kind: task.KindAction, State: "inbox", Category: task.CategoryInbox, Title: "both", Units: []unit.Unit{r7u, slackUnit}})
	var v api.TaskView
	if code := get(t, r.s, api.TaskPath("e-mixed"), &v); code != 200 {
		t.Fatalf("GET: %d", code)
	}
	for _, vs := range v.Verbs {
		if vs.Verb == "dismiss" && (vs.Enabled || vs.Reason == "" || vs.Reason == "slack is disabled") {
			t.Errorf("dismiss follows the row of %s: %+v", r7u.Label(), vs)
		}
	}
	if rec := post(t, r.s, api.TaskPath("e-mixed", "dismiss"), `{"source":"cockpit"}`); rec.Code != 409 || strings.Contains(rec.Body.String(), `"plugin":"slack"`) {
		t.Fatalf("dismiss: %d %s", rec.Code, rec.Body)
	}
	if got, err := r.g.Task(context.Background(), "e-mixed"); err != nil || len(got.Units) != 2 || got.State != task.State("inbox") {
		t.Fatalf("untouched %+v %v", got, err)
	}
}

func TestTheDraftsRouteDiscardsADisabledPluginsDraft(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t, "slack")
	d := r.stored(t, draft.Draft{Unit: slackUnit, Text: "hi", State: draft.Proposed})
	var list api.DraftsResponse
	if code := get(t, r.s, api.Drafts, &list); code != 200 || len(list.Drafts) != 1 {
		t.Fatalf("GET: %d %+v", code, list)
	}
	for _, st := range list.Drafts[0].Actions {
		if st.Enabled != (st.ID == "draft.discard") {
			t.Errorf("listed %+v", st)
		}
	}
	if code, _, body := r.act(t, d.ID, "edit", `{"text":"x"}`); code != 409 {
		t.Fatalf("edit: %d %s", code, body)
	}
	code, got, body := r.act(t, d.ID, "discard", `{}`)
	if code != 200 || got.State != string(draft.Discarded) {
		t.Fatalf("discard: %d %s", code, body)
	}
}
