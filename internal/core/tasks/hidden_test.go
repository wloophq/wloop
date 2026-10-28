package tasks_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestADisabledPluginsTasksLeaveTheViewsAndCountsButStayDismissable(t *testing.T) {
	r := newRig()
	var off []unit.ProviderID
	r.svc = coretasks.New(coretasks.Deps{Store: r.store, Items: r.items, Facts: r.facts, Me: me, Clock: r.clock,
		Disabled: func() []unit.ProviderID { return off }})
	ctx := context.Background()
	slack := unit.Unit{Provider: "slack", Key: "C1/1.1"}
	r.put(t, task.Task{ID: "e-gh", Kind: task.KindMessage, State: task.Inbox, Units: []unit.Unit{unitU}, Origin: task.OriginMessage})
	r.put(t, task.Task{ID: "e-next", Kind: task.KindAction, State: task.Next, Units: []unit.Unit{unitV}, Origin: task.OriginUser})
	r.put(t, task.Task{ID: "e-wait", Kind: task.KindAction, State: task.Waiting, Units: []unit.Unit{unitV}, Origin: task.OriginUser})
	r.put(t, task.Task{ID: "e-both", Kind: task.KindAction, State: task.Inbox, Units: []unit.Unit{unitU, slack}, Origin: task.OriginUser})
	r.put(t, task.Task{ID: "e-mine", Kind: task.KindAction, State: task.Inbox, Origin: task.OriginUser})
	ids := func(v task.View) []task.ID {
		t.Helper()
		es, err := r.svc.Tasks(ctx, task.Query{View: v})
		if err != nil {
			t.Fatal(err)
		}
		var out []task.ID
		for _, e := range es {
			out = append(out, e.ID)
		}
		return out
	}
	counts := func() task.Counters {
		t.Helper()
		c, err := r.svc.Counts(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	if c := counts(); c.Badge != 3 || c.Views[task.ViewNext] != 1 {
		t.Fatalf("enabled: %+v", c)
	}
	off, r.facts.off[unit.GitHub] = []unit.ProviderID{unit.GitHub}, true
	if got := ids(task.ViewInbox); len(got) != 2 || got[0] == "e-gh" || got[1] == "e-gh" {
		t.Fatalf("inbox %v", got)
	}
	if got := ids(task.ViewNext); len(got) != 0 {
		t.Fatalf("next %v", got)
	}
	if got := ids(task.ViewWaiting); len(got) != 1 {
		t.Fatalf("waiting keeps it read-only: %v", got)
	}
	c := counts()
	if c.Badge != 2 || c.Views[task.ViewInbox] != 2 || c.Views[task.ViewNext] != 0 || c.Views[task.ViewWaiting] != 1 {
		t.Fatalf("counts %+v", c)
	}
	p, err := r.svc.TasksSince(ctx, task.Query{View: task.ViewInbox}, coretasks.Since{})
	if err != nil || len(p.Tasks) != 2 || p.Counters.Badge != 2 {
		t.Fatalf("page %+v %v", p, err)
	}
	if _, err := r.svc.Do(ctx, "e-gh", coretasks.Request{Verb: task.VerbNext}); !errors.Is(err, coretasks.ErrPluginDisabled) {
		t.Fatalf("next: %v", err)
	}
	if res, err := r.svc.Do(ctx, "e-gh", coretasks.Request{Verb: task.VerbDismiss}); err != nil || res.Task.State != task.Trash {
		t.Fatalf("dismiss: %+v %v", res, err)
	}
	if res, err := r.svc.Do(ctx, "e-both", coretasks.Request{Verb: task.VerbDismiss}); err != nil || res.Task.State != task.Inbox ||
		len(res.Task.Units) != 1 || res.Task.Units[0].Provider != "slack" {
		t.Fatalf("dismiss of an entry with a unit of an enabled plugin detaches the disabled one: %+v %v", res, err)
	}
	if got := r.task(t, "e-next"); got.State != task.Next {
		t.Fatalf("data kept: %+v", got)
	}
	off, r.facts.off[unit.GitHub] = nil, false
	if got := ids(task.ViewNext); len(got) != 1 {
		t.Fatalf("enabled again: %v", got)
	}
}

func TestDismissDetachesADisabledPluginsUnitsFromAMixedTask(t *testing.T) {
	r := newRig()
	off := []unit.ProviderID{"slack"}
	r.svc = coretasks.New(coretasks.Deps{Store: r.store, Items: r.items, Facts: r.facts, Me: me, Clock: r.clock,
		Disabled: func() []unit.ProviderID { return off }})
	r.facts.off["slack"] = true
	ctx := context.Background()
	slack := unit.Unit{Provider: "slack", Key: "C1/1.1"}
	r.put(t, task.Task{ID: "e-mixed", Kind: task.KindAction, State: task.Next, Units: []unit.Unit{unitU, slack},
		Items: []task.ItemRef{{Unit: unitU, Item: "a"}, {Unit: slack, Item: "b"}}, Origin: task.OriginUser})
	r.put(t, task.Task{ID: "e-two", Kind: task.KindAction, State: task.Inbox, Units: []unit.Unit{slack, unitV},
		Origin: task.OriginUser})

	_, err := r.svc.Do(ctx, "e-mixed", coretasks.Request{Verb: task.VerbDone})
	if !errors.Is(err, coretasks.ErrPluginDisabled) || !strings.Contains(err.Error(), "slack") {
		t.Fatalf("done names the disabled plugin: %v", err)
	}
	res, err := r.svc.Do(ctx, "e-mixed", coretasks.Request{Verb: task.VerbDismiss})
	if err != nil {
		t.Fatal(err)
	}
	got := r.task(t, "e-mixed")
	if got.State != task.Next || !slices.Equal(got.Units, []unit.Unit{unitU}) ||
		!slices.Equal(got.Items, []task.ItemRef{{Unit: unitU, Item: "a"}}) || res.Task.ID != got.ID || !slices.Equal(res.Task.Units, got.Units) {
		t.Fatalf("detached %+v, result %+v", got, res.Task)
	}
	if res, err := r.svc.Do(ctx, "e-mixed", coretasks.Request{Verb: task.VerbDone}); err != nil || res.Task.State != task.Done {
		t.Fatalf("done once detached: %+v %v", res, err)
	}
	if _, err := r.svc.Do(ctx, "e-two", coretasks.Request{Verb: task.VerbDismiss}); err != nil {
		t.Fatal(err)
	}
	if got := r.task(t, "e-two"); got.State != task.Inbox || !slices.Equal(got.Units, []unit.Unit{unitV}) {
		t.Fatalf("e-two %+v", got)
	}
}

func TestDismissTrashesATaskWithNoUnitOfAnEnabledPluginWhole(t *testing.T) {
	r := newRig()
	r.facts.off[unit.GitHub] = true
	r.put(t, task.Task{ID: "e-gh", Kind: task.KindAction, State: task.Inbox, Units: []unit.Unit{unitU},
		Items: []task.ItemRef{{Unit: unitU, Item: "a"}}, Origin: task.OriginUser})
	res, err := r.svc.Do(context.Background(), "e-gh", coretasks.Request{Verb: task.VerbDismiss})
	got := r.task(t, "e-gh")
	if err != nil || res.Task.State != task.Trash || got.State != task.Trash || len(got.Units) != 1 || len(got.Items) != 1 {
		t.Fatalf("trashed whole: %+v %+v %v", got, res, err)
	}
}

func TestDismissTrashesADerivedMixedTaskWhole(t *testing.T) {
	r := newRig()
	r.facts.off["slack"] = true
	slack := unit.Unit{Provider: "slack", Key: "C1/1.1"}
	id := task.DerivedID(task.KindAction, slack, "i1")
	r.put(t, task.Task{ID: id, Kind: task.KindAction, State: task.Next, Units: []unit.Unit{slack, unitU},
		Items: []task.ItemRef{{Unit: slack, Item: "i1"}, {Unit: unitU, Item: "a"}}, Origin: task.OriginDerived})
	if _, err := r.svc.Do(context.Background(), id, coretasks.Request{Verb: task.VerbDismiss}); err != nil {
		t.Fatal(err)
	}
	if got := r.task(t, id); got.State != task.Trash || len(got.Units) != 2 || len(got.Items) != 2 {
		t.Fatalf("a derived entry keeps the unit its id names: %+v", got)
	}
}

func TestDetachDropsItemsOnAnyUnitOfTheDisabledPlugin(t *testing.T) {
	r := newRig()
	r.facts.off["slack"] = true
	a, b := unit.Unit{Provider: "slack", Key: "C1/1.1"}, unit.Unit{Provider: "slack", Key: "C1/2.2"}
	r.put(t, task.Task{ID: "e-mixed", Kind: task.KindAction, State: task.Inbox, Units: []unit.Unit{unitU, a},
		Items: []task.ItemRef{{Unit: unitU, Item: "x"}, {Unit: b, Item: "y"}}, Origin: task.OriginUser})
	if _, err := r.svc.Do(context.Background(), "e-mixed", coretasks.Request{Verb: task.VerbDismiss}); err != nil {
		t.Fatal(err)
	}
	if got := r.task(t, "e-mixed"); !slices.Equal(got.Items, []task.ItemRef{{Unit: unitU, Item: "x"}}) {
		t.Fatalf("items %+v", got.Items)
	}
}

func TestDetachDropsItemsOfADisabledPluginWithNoUnitOnTheTask(t *testing.T) {
	r := newRig()
	r.facts.off["slack"], r.facts.off["jira"] = true, true
	slack, ticket := unit.Unit{Provider: "slack", Key: "C1/1.1"}, unit.Unit{Provider: "jira", Key: "T-1"}
	r.put(t, task.Task{ID: "e-mixed", Kind: task.KindAction, State: task.Inbox, Units: []unit.Unit{unitU, slack},
		Items: []task.ItemRef{{Unit: unitU, Item: "x"}, {Unit: ticket, Item: "y"}}, Origin: task.OriginUser})
	if _, err := r.svc.Do(context.Background(), "e-mixed", coretasks.Request{Verb: task.VerbDismiss}); err != nil {
		t.Fatal(err)
	}
	if got := r.task(t, "e-mixed"); !slices.Equal(got.Units, []unit.Unit{unitU}) || !slices.Equal(got.Items, []task.ItemRef{{Unit: unitU, Item: "x"}}) {
		t.Fatalf("%+v", got)
	}
}
