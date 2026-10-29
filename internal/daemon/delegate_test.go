package daemon

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/outbox"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel"
)

// unitTask is an Inbox task of origin on units, with one item on each.
func unitTask(id, origin string, units ...unit.Unit) task.Task {
	e := task.Task{ID: task.ID(id), Origin: origin, State: "inbox", Category: task.CategoryInbox, Title: "Task " + id, Units: units}
	for _, u := range units {
		e.Items = append(e.Items, task.ItemRef{Unit: u, Item: "it-" + id})
	}
	return e
}

// newDelegateRig serves the tasks over a thread provider whose rows are read like Slack's.
func newDelegateRig(t *testing.T) (*tasksRig, *startedHeadless, *fakeActions) {
	t.Helper()
	h, fa := &startedHeadless{}, &fakeActions{}
	r := newTasksRig(t, func(d *Deps) {
		d.Headless, d.Actions = h, fa
		d.Units = unit.NewRegistry(pr.Codec{}, unit.OpaqueCodec{ID: "thread"})
		d.RowProviders = append(d.RowProviders, func(readmodel.RowDeps) readmodel.Provider { return threadRows{units: []unit.Unit{actThread}} })
	})
	return r, h, fa
}

func verbState(v api.TaskView, verb string) api.VerbState {
	i := slices.IndexFunc(v.Verbs, func(s api.VerbState) bool { return s.Verb == verb })
	if i < 0 {
		return api.VerbState{}
	}
	return v.Verbs[i]
}

func TestDelegateOnAThreadTaskRunsActWithGuidanceAndMode(t *testing.T) {
	t.Parallel()
	r, h, fa := newDelegateRig(t)
	r.put(t, unitTask("t-a", task.OriginDerived, actThread), unitTask("t-w", task.OriginDerived, actThread),
		unitTask("t-c", task.OriginDerived, actThread), unitTask("t-n", task.OriginUser))
	for _, id := range []string{"t-a", "t-w", "t-c"} {
		var v api.TaskView
		if code := get(t, r.s, api.TaskPath(id), &v); code != 200 || !verbState(v, "delegate").Enabled {
			t.Fatalf("%s: %d delegate %+v", id, code, verbState(v, "delegate"))
		}
	}
	var none api.TaskView
	if get(t, r.s, api.TaskPath("t-n"), &none); verbState(none, "delegate").Enabled {
		t.Fatalf("delegate offered on a capture with no unit: %+v", none.Verbs)
	}
	rec := post(t, r.s, api.TaskPath("t-a", "delegate"), `{"source":"cockpit","args":{"guidance":"keep it short","mode":"headless"}}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "act started headless as turn 42") {
		t.Fatalf("headless: %d %s", rec.Code, rec.Body)
	}
	if len(h.got) != 1 || h.got[0].Guidance != "keep it short" || h.got[0].Task != "t-a" || !slices.Equal(h.got[0].Items, []string{"it-t-a"}) ||
		h.got[0].Intent != turn.Intent(dintent.Act) {
		t.Fatalf("started %+v", h.got)
	}
	if rec := post(t, r.s, api.TaskPath("t-w", "delegate"), `{"source":"tui"}`); rec.Code != 200 || len(h.got) != 2 || h.got[1].Task != "t-w" {
		t.Fatalf("a waiting task, no mode: %d %s %+v", rec.Code, rec.Body, h.got)
	}
	if rec := post(t, r.s, api.TaskPath("t-c", "delegate"), `{"source":"cockpit","args":{"guidance":"close it","mode":"session"}}`); rec.Code != 200 ||
		len(fa.calls) != 1 || !strings.HasPrefix(fa.calls[0], "act ") || len(h.got) != 2 || !slices.Equal(fa.bound, []string{"it-t-c@t-c"}) {
		t.Fatalf("session: %d %s, calls %v, bound %v", rec.Code, rec.Body, fa.calls, fa.bound)
	}
	if rec := post(t, r.s, api.TaskPath("t-a", "delegate"), `{"source":"cockpit","args":{"mode":"later"}}`); rec.Code != 400 || !strings.Contains(rec.Body.String(), "mode is headless or session") {
		t.Fatalf("an unknown mode: %d %s", rec.Code, rec.Body)
	}
}

func TestAThreadInboxNotificationOffersDraftForMeAndReply(t *testing.T) {
	t.Parallel()
	r, _, _ := newDelegateRig(t)
	r.s.deps.Outbox = &outbox.Service{Store: r.db.Drafts(), Clock: fixedClock(tasksNow)}
	r.put(t, unitTask("t-a", task.OriginDerived, actThread), unitTask("t-n", task.OriginUser))
	var v api.TaskView
	if code := get(t, r.s, api.TaskPath("t-a"), &v); code != 200 || v.Attention == nil {
		t.Fatalf("%d %+v", code, v.Attention)
	}
	var ids []string
	for _, a := range v.Attention.Actions {
		ids = append(ids, a.ID)
		if a.TextInput != (a.ID != api.AttentionDraftForMe) {
			t.Errorf("%s: text input %v", a.ID, a.TextInput)
		}
	}
	if want := []string{api.AttentionDraftForMe, api.AttentionSendReply, api.AttentionReply}; !slices.Equal(ids, want) {
		t.Fatalf("actions %v, want %v", ids, want)
	}
	if a := v.Attention.Actions; a[2].Placeholder != "Your reply, saved for your approval" || a[2].Button != "Save" ||
		a[1].Placeholder != "Your reply, sent now" || a[1].Button != "Send" || a[0].Placeholder != "" {
		t.Errorf("the inputs are not worded by the notices: %+v", a)
	}
	if get(t, r.s, api.TaskPath("t-n"), &v); v.Attention == nil || len(v.Attention.Actions) != 0 {
		t.Fatalf("a capture with no unit: %+v", v.Attention)
	}
	rec := post(t, r.s, api.TaskPath("t-a", "delegate"), `{"source":"menubar","args":{"mode":"headless"}}`)
	if rec.Code != 200 {
		t.Fatalf("Draft for me: %d %s", rec.Code, rec.Body)
	}
}

func TestDelegateRefusesAModeTheActionDoesNotRun(t *testing.T) {
	t.Parallel()
	c, _ := bridgeRig(t)
	rec := post(t, c.s, api.TaskPath("t-r", "delegate"), `{"source":"cockpit","args":{"mode":"headless"}}`)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "review does not run in headless mode; hand it to an agent instead") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := post(t, c.s, api.TaskPath("t-r", "delegate"), `{"source":"cockpit","args":{"mode":"session"}}`); rec.Code != 200 {
		t.Fatalf("session: %d %s", rec.Code, rec.Body)
	}
}

type placerFunc func(unit.Unit) (string, error)

func (f placerFunc) SessionPlace(_ context.Context, u unit.Unit) (string, error) { return f(u) }

func TestDelegateSaysUpfrontWhetherASessionCanRun(t *testing.T) {
	t.Parallel()
	r, _, fa := newDelegateRig(t)
	r.put(t, unitTask("t-a", task.OriginDerived, actThread))
	modes := func() []api.RunnerState {
		var v api.TaskView
		if code := get(t, r.s, api.TaskPath("t-a"), &v); code != 200 {
			t.Fatalf("%d", code)
		}
		return verbState(v, "delegate").Runners
	}
	r.s.deps.Placer = placerFunc(func(unit.Unit) (string, error) { return "a neutral directory with no checkout", nil })
	if got := modes(); !slices.Equal(got, []api.RunnerState{{Runner: "headless", Enabled: true}, {Runner: "session", Enabled: true}}) {
		t.Fatalf("placed: %+v", got)
	}
	r.s.deps.Placer = placerFunc(func(unit.Unit) (string, error) { return "", errors.New("no workspace manager is enabled") })
	want := []api.RunnerState{{Runner: "headless", Enabled: true}, {Runner: "session", Reason: "no workspace manager is enabled"}}
	if got := modes(); !slices.Equal(got, want) {
		t.Fatalf("unplaceable: %+v", got)
	}
	rec := post(t, r.s, api.TaskPath("t-a", "delegate"), `{"source":"cockpit","args":{"mode":"session"}}`)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "no workspace manager") || len(fa.calls) != 0 {
		t.Fatalf("session refused upfront: %d %s, calls %v", rec.Code, rec.Body, fa.calls)
	}
}
