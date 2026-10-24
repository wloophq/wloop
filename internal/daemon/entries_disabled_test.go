package daemon

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestADisabledPluginsEntriesAndDraftsLeaveTheCountsAndStayDismissable(t *testing.T) {
	r := newOutboxRig(t, unit.GitHub, "slack")
	ctx := context.Background()
	r.put(t, gtd.Entry{ID: "e-gh", Kind: gtd.KindAction, State: gtd.Inbox, Title: "reply", Units: []unit.Unit{r7u}})
	r.put(t, gtd.Entry{ID: "e-mine", Kind: gtd.KindAction, State: gtd.Inbox, Title: "mine"})
	d := r.stored(t, draft.Draft{Unit: slackUnit, Text: "hi", State: draft.Proposed})

	inbox := r.page(t, "view=inbox")
	if got := entryIDs(inbox.Entries); len(got) != 1 || got[0] != "e-mine" {
		t.Fatalf("inbox %v", got)
	}
	if c := inbox.Counters; c.Badge != 1 || c.Views["inbox"] != 1 || c.Views["outbox"] != 0 {
		t.Fatalf("counters %+v", c)
	}
	if p := r.page(t, "view=outbox"); len(p.Entries) != 0 {
		t.Fatalf("outbox %v", entryIDs(p.Entries))
	}
	var v api.EntryView
	if code := get(t, r.s, api.EntryPath("e-gh"), &v); code != 200 {
		t.Fatalf("GET by id: %d", code)
	}
	for _, vs := range v.Verbs {
		if vs.Enabled != (vs.Verb == "dismiss") {
			t.Errorf("%+v", vs)
		}
	}
	if rec := post(t, r.s, api.EntryPath("e-gh", "next"), `{"source":"cockpit"}`); rec.Code != 409 {
		t.Fatalf("next: %d %s", rec.Code, rec.Body)
	}
	if rec := post(t, r.s, api.EntryPath("e-gh", "dismiss"), `{"source":"cockpit"}`); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"state":"trash"`) {
		t.Fatalf("dismiss: %d %s", rec.Code, rec.Body)
	}
	var dv api.EntryView
	if code := get(t, r.s, api.EntryPath(fmt.Sprintf("draft:%d", d.ID)), &dv); code != 200 {
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

func TestAMixedEntryIsNeitherHiddenNorDismissedWithoutItsPlugin(t *testing.T) {
	r := newOutboxRig(t, "slack")
	r.put(t, gtd.Entry{ID: "e-mixed", Kind: gtd.KindAction, State: gtd.Inbox, Title: "both", Units: []unit.Unit{r7u, slackUnit}})
	if got := entryIDs(r.page(t, "view=inbox").Entries); len(got) != 1 || got[0] != "e-mixed" {
		t.Fatalf("inbox %v", got)
	}
	var v api.EntryView
	if code := get(t, r.s, api.EntryPath("e-mixed"), &v); code != 200 {
		t.Fatalf("GET: %d", code)
	}
	for _, vs := range v.Verbs {
		if vs.Enabled {
			t.Errorf("%+v", vs)
		}
	}
	if rec := post(t, r.s, api.EntryPath("e-mixed", "dismiss"), `{"source":"cockpit"}`); rec.Code != 409 || !strings.Contains(rec.Body.String(), `"plugin":"slack"`) {
		t.Fatalf("dismiss: %d %s", rec.Code, rec.Body)
	}
}

func TestTheDraftsRouteDiscardsADisabledPluginsDraft(t *testing.T) {
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
