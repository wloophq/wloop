package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func taskResult(t *testing.T, body []byte) api.TaskResult {
	t.Helper()
	var res api.TaskResult
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("%v %s", err, body)
	}
	return res
}

func (r *outboxRig) drafts(t *testing.T) []draft.Draft {
	t.Helper()
	ds, err := r.o.List(context.Background(), outbox.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	return ds
}

func TestReplySavesAUserDraftToTheOutbox(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	r.put(t, unitTask("t-1", task.OriginDerived, r7u))
	var v api.TaskView
	if code := get(t, r.s, api.TaskPath("t-1"), &v); code != 200 ||
		!slices.ContainsFunc(v.Verbs, func(s api.VerbState) bool { return s.Verb == "reply" && s.Enabled }) {
		t.Fatalf("%d %+v", code, v.Verbs)
	}
	rec := post(t, r.s, api.TaskPath("t-1", "reply"), `{"source":"tui","args":{"text":"On it, thanks","send":"draft"}}`)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if res := taskResult(t, rec.Body.Bytes()); len(res.Lines) != 1 || res.Lines[0] != "Draft 1 saved for your approval." || res.Task.ID != "t-1" {
		t.Fatalf("result %+v", res)
	}
	ds := r.drafts(t)
	if len(ds) != 1 {
		t.Fatalf("drafts %+v", ds)
	}
	d := ds[0]
	if d.Unit != r7u || d.Task != "t-1" || !slices.Equal(d.Items, []string{"it-t-1"}) || d.Kind != draft.Reply || d.Text != "On it, thanks" ||
		d.Origin.Kind != draft.OriginUser || d.State != draft.Held {
		t.Fatalf("draft %+v", d)
	}
	var listed api.DraftsResponse
	if code := get(t, r.s, api.Drafts+"?task=t-1", &listed); code != 200 || len(listed.Drafts) != 1 || listed.Drafts[0].Origin.Kind != "user" {
		t.Fatalf("drafts of the task: %d %+v", code, listed)
	}
	evs, err := r.db.Events().Since(context.Background(), 0, []string{task.VerbEventKind(task.VerbReply)})
	if err != nil || len(evs) != 1 || evs[0].Unit != task.UnitOf("t-1") {
		t.Fatalf("reply events %+v %v", evs, err)
	}
}

func TestAReplyThatNamesNoSendTakesItsVerbsSend(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	r.put(t, unitTask("t-1", task.OriginDerived, r7u))
	rec := post(t, r.s, api.TaskPath("t-1", "reply"), `{"source":"cockpit","args":{"text":"Shipped"}}`)
	if res := taskResult(t, rec.Body.Bytes()); rec.Code != 200 || len(res.Lines) != 1 || res.Lines[0] != "Draft 1 approved; it goes out in a moment." {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if d := r.drafts(t)[0]; d.State != draft.Queued {
		t.Fatalf("the bundled send now did not send: %+v", d)
	}
	r.s.deps.Intents = draftReplies(t)
	rec = post(t, r.s, api.TaskPath("t-1", "reply"), `{"source":"cockpit","args":{"text":"Later"}}`)
	if res := taskResult(t, rec.Body.Bytes()); rec.Code != 200 || len(res.Lines) != 1 || res.Lines[0] != "Draft 2 saved for your approval." {
		t.Fatalf("a profile with send draft: %d %s", rec.Code, rec.Body)
	}
}

func TestReplySendNowApprovesThroughTheOutbox(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	r.put(t, unitTask("t-1", task.OriginDerived, r7u))
	rec := post(t, r.s, api.TaskPath("t-1", "reply"), `{"source":"notification","args":{"text":"Ping","send":"now"}}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "approved; it goes out") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	d := r.drafts(t)[0]
	if g, _, _ := r.o.GateOf(context.Background(), d); d.State != draft.Queued || g.Status != gate.Approved || g.By != task.ActorPerson || d.Origin.Kind != draft.OriginUser {
		t.Fatalf("draft %+v, gate %+v", d, g)
	}
	sent, failed, err := r.o.Send(context.Background())
	if err != nil || sent != 1 || failed != 0 || !slices.Equal(r.fx.seen(), []string{fmt.Sprintf("publish %d", d.ID)}) {
		t.Fatalf("send: %d %d %v, publisher %v", sent, failed, err, r.fx.seen())
	}
}

func TestReplyRefusals(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	r.put(t, unitTask("t-none", task.OriginUser), unitTask("t-msg", task.OriginMessage, r7u), unitTask("t-1", task.OriginDerived, r7u),
		unitTask("t-s", task.OriginDerived, slackUnit))
	for _, c := range []struct {
		name, id, body string
		code           int
		says           string
	}{
		{"no unit", "t-none", `{"source":"cockpit","args":{"text":"x"}}`, 409, "no conversation"},
		{"a message", "t-msg", `{"source":"cockpit","args":{"text":"x"}}`, 409, "takes no reply"},
		{"empty", "t-1", `{"source":"cockpit","args":{"text":"  "}}`, 400, "empty text"},
		{"bad send", "t-1", `{"source":"cockpit","args":{"text":"x","send":"yes"}}`, 400, "send is"},
		{"bad source", "t-1", `{"source":"agent","args":{"text":"x"}}`, 400, "source"},
		{"text on a reaction", "t-1", `{"source":"cockpit","args":{"kind":"react","text":"x","reaction":"eyes"}}`, 400, "takes an emoji"},
		{"no publisher", "t-s", `{"source":"cockpit","args":{"text":"x"}}`, 409, "publisher"},
	} {
		rec := post(t, r.s, api.TaskPath(c.id, "reply"), c.body)
		if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.says) {
			t.Errorf("%s: %d %s", c.name, rec.Code, rec.Body)
		}
	}
	if ds := r.drafts(t); len(ds) != 0 {
		t.Fatalf("drafts %+v", ds)
	}
	var v api.TaskView
	if get(t, r.s, api.TaskPath("t-none"), &v); slices.ContainsFunc(v.Verbs, func(s api.VerbState) bool { return s.Verb == "reply" && s.Enabled }) {
		t.Fatalf("reply offered on a task with no unit: %+v", v.Verbs)
	}
}

func TestDraftCreateRouteWritesOnAUnit(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	r.put(t, unitTask("t-1", task.OriginDerived, r7u))
	rec := post(t, r.s, api.Drafts, `{"unit":{"provider":"github","key":"acme/api#7"},"text":"LGTM","source":"cockpit"}`)
	var d api.Draft
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &d) != nil || d.State != "held" || d.Origin.Kind != "user" || d.Task != "" || d.Kind != "reply" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	rec = post(t, r.s, api.Drafts, `{"unit":{"provider":"github","key":"acme/api#7"},"task":"t-1","items":["it-t-1"],"text":"Done","send":true,"source":"cockpit"}`)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &d) != nil || d.State != "queued" || d.Task != "t-1" || !slices.Equal(d.Items, []string{"it-t-1"}) {
		t.Fatalf("send: %d %s", rec.Code, rec.Body)
	}
	for _, c := range []struct {
		name, body string
		code       int
	}{
		{"no unit", `{"text":"x","source":"cockpit"}`, 400},
		{"another unit's task", `{"unit":{"provider":"github","key":"acme/api#9"},"task":"t-1","text":"x","source":"cockpit"}`, 400},
		{"no such task", `{"unit":{"provider":"github","key":"acme/api#7"},"task":"t-9","text":"x","source":"cockpit"}`, 404},
		{"an unknown kind", `{"unit":{"provider":"github","key":"acme/api#7"},"kind":"nudge","text":"x","source":"cockpit"}`, 400},
	} {
		if rec := post(t, r.s, api.Drafts, c.body); rec.Code != c.code {
			t.Errorf("%s: %d %s", c.name, rec.Code, rec.Body)
		}
	}
}

func TestDraftCreateRefusesADisabledPlugin(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t, unit.GitHub)
	rec := post(t, r.s, api.Drafts, `{"unit":{"provider":"github","key":"acme/api#7"},"text":"x","source":"cockpit"}`)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), `"plugin":"github"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestDraftCreateRefusesATargetOutsideItsUnit(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	r.s.deps.Resolve = func(_ context.Context, address string) (unit.Unit, bool, error) {
		if address == "acme/api#7 review" {
			return r7u, true, nil
		}
		return unit.Unit{}, false, nil
	}
	for _, c := range []struct {
		name, target string
		code         int
	}{
		{"a message of its unit", "acme/api#7 review", 200},
		{"a message of another unit", "acme/api#9 review", 400},
		{"an address no plugin reads", "#general", 400},
	} {
		body := `{"unit":{"provider":"github","key":"acme/api#7"},"text":"x","target":"` + c.target + `","source":"terminal"}`
		if rec := post(t, r.s, api.Drafts, body); rec.Code != c.code {
			t.Errorf("%s: %d %s", c.name, rec.Code, rec.Body)
		}
	}
	if ds := r.drafts(t); len(ds) != 1 || ds[0].Target != "acme/api#7 review" {
		t.Fatalf("drafts %+v", ds)
	}
}

func TestAnAgentSessionSendsNoDraftAtOnce(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	r.put(t, unitTask("t-1", task.OriginDerived, r7u))
	rec := agentCall(t, r.s.Handler(), "POST", api.Drafts, "term_1", `{"unit":{"provider":"github","key":"acme/api#7"},"text":"Done","send":true,"source":"cockpit"}`)
	if rec.Code != 403 {
		t.Fatalf("drafts send from a session: %d %s", rec.Code, rec.Body)
	}
	rec = agentCall(t, r.s.Handler(), "POST", api.TaskPath("t-1", "reply"), "term_1", `{"source":"cockpit","args":{"text":"Done","send":"now"}}`)
	if rec.Code != 403 {
		t.Fatalf("reply now from a session: %d %s", rec.Code, rec.Body)
	}
	if ds := r.drafts(t); len(ds) != 0 {
		t.Fatalf("a session's send left drafts %+v", ds)
	}
	if _, _, err := r.o.Send(context.Background()); err != nil || len(r.fx.seen()) != 0 {
		t.Fatalf("published %v %v", r.fx.seen(), err)
	}
}
