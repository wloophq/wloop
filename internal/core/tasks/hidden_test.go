package tasks_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// TestDisabledPluginTasksAreHidden checks that a view the profile marks hide_disabled leaves out, in its list,
// its page and its count, the tasks whose units are all of a disabled plugin, that a view not marked keeps
// them, and that they stay dismissable.
func TestDisabledPluginTasksAreHidden(t *testing.T) {
	r := newRig()
	var off []unit.ProviderID
	r.svc = coretasks.New(coretasks.Deps{Store: r.store, Items: r.items, Facts: r.facts, Me: me, Clock: r.clock, States: taskstest.BundledStates,
		Disabled: func() []unit.ProviderID { return off }, Views: r.lens.Views, ViewEval: r.lens.Eval})
	ctx := context.Background()
	slack := unit.Unit{Provider: "slack", Key: "C1/1.1"}
	r.put(t, task.Task{ID: "t-gh", State: "inbox", Category: task.CategoryInbox, Units: []unit.Unit{unitU}, Origin: task.OriginMessage})
	r.put(t, task.Task{ID: "t-next", State: "next", Category: task.CategoryReady, Units: []unit.Unit{unitV}, Origin: task.OriginUser})
	r.put(t, task.Task{ID: "t-wait", State: "waiting", Category: task.CategoryWaiting, Units: []unit.Unit{unitV}, Origin: task.OriginUser})
	r.put(t, task.Task{ID: "t-both", State: "inbox", Category: task.CategoryInbox, Units: []unit.Unit{unitU, slack}, Origin: task.OriginUser})
	r.put(t, task.Task{ID: "t-mine", State: "inbox", Category: task.CategoryInbox, Origin: task.OriginUser})
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
	if c := counts(); c.Badge != 3 || c.Views["ready"] != 1 {
		t.Fatalf("enabled: %+v", c)
	}
	off, r.facts.off[unit.GitHub] = []unit.ProviderID{unit.GitHub}, true
	if got := ids("inbox"); len(got) != 2 || got[0] == "t-gh" || got[1] == "t-gh" {
		t.Fatalf("inbox %v", got)
	}
	if got := ids("ready"); len(got) != 0 {
		t.Fatalf("next %v", got)
	}
	if got := ids("waiting"); len(got) != 1 {
		t.Fatalf("waiting keeps it read-only: %v", got)
	}
	c := counts()
	if c.Badge != 2 || c.Views["inbox"] != 2 || c.Views["ready"] != 0 || c.Views["waiting"] != 1 {
		t.Fatalf("counts %+v", c)
	}
	p, err := r.svc.TasksSince(ctx, task.Query{View: "inbox"}, coretasks.Since{})
	if err != nil || len(p.Tasks) != 2 || p.Counters.Badge != 2 {
		t.Fatalf("page %+v %v", p, err)
	}
	if _, err := r.svc.Do(ctx, "t-gh", coretasks.Request{Actor: task.ActorPerson, Verb: task.VerbNext}); !errors.Is(err, coretasks.ErrPluginDisabled) {
		t.Fatalf("next: %v", err)
	}
	if res, err := r.svc.Do(ctx, "t-gh", coretasks.Request{Actor: task.ActorPerson, Verb: task.VerbDismiss}); err != nil || res.Task.Category != task.CategoryDropped {
		t.Fatalf("dismiss: %+v %v", res, err)
	}
	if res, err := r.svc.Do(ctx, "t-both", coretasks.Request{Actor: task.ActorPerson, Verb: task.VerbDismiss}); err != nil || res.Task.Category != task.CategoryInbox ||
		len(res.Task.Units) != 1 || res.Task.Units[0].Provider != "slack" {
		t.Fatalf("dismiss of a task with a unit of an enabled plugin detaches the disabled one: %+v %v", res, err)
	}
	if got := r.task(t, "t-next"); got.Category != task.CategoryReady {
		t.Fatalf("data kept: %+v", got)
	}
	off, r.facts.off[unit.GitHub] = nil, false
	if got := ids("ready"); len(got) != 1 {
		t.Fatalf("enabled again: %v", got)
	}
}

func TestDismissDetachesADisabledPluginsUnitsFromAMixedTask(t *testing.T) {
	r := newRig()
	off := []unit.ProviderID{"slack"}
	r.svc = coretasks.New(coretasks.Deps{Store: r.store, Items: r.items, Facts: r.facts, Me: me, Clock: r.clock, States: taskstest.BundledStates,
		Disabled: func() []unit.ProviderID { return off }})
	r.facts.off["slack"] = true
	ctx := context.Background()
	slack := unit.Unit{Provider: "slack", Key: "C1/1.1"}
	r.put(t, task.Task{ID: "t-mixed", State: "next", Category: task.CategoryReady, Units: []unit.Unit{unitU, slack},
		Items: []task.ItemRef{{Unit: unitU, Item: "a"}, {Unit: slack, Item: "b"}}, Origin: task.OriginUser})
	r.put(t, task.Task{ID: "t-two", State: "inbox", Category: task.CategoryInbox, Units: []unit.Unit{slack, unitV},
		Origin: task.OriginUser})

	_, err := r.svc.Do(ctx, "t-mixed", coretasks.Request{Actor: task.ActorPerson, Verb: task.VerbDone})
	if !errors.Is(err, coretasks.ErrPluginDisabled) || !strings.Contains(err.Error(), "slack") {
		t.Fatalf("done names the disabled plugin: %v", err)
	}
	res, err := r.svc.Do(ctx, "t-mixed", coretasks.Request{Actor: task.ActorPerson, Verb: task.VerbDismiss})
	if err != nil {
		t.Fatal(err)
	}
	got := r.task(t, "t-mixed")
	if got.Category != task.CategoryReady || !slices.Equal(got.Units, []unit.Unit{unitU}) ||
		!slices.Equal(got.Items, []task.ItemRef{{Unit: unitU, Item: "a"}}) || res.Task.ID != got.ID || !slices.Equal(res.Task.Units, got.Units) {
		t.Fatalf("detached %+v, result %+v", got, res.Task)
	}
	if res, err := r.svc.Do(ctx, "t-mixed", coretasks.Request{Actor: task.ActorPerson, Verb: task.VerbDone}); err != nil || res.Task.Category != task.CategoryDone {
		t.Fatalf("done once detached: %+v %v", res, err)
	}
	if _, err := r.svc.Do(ctx, "t-two", coretasks.Request{Actor: task.ActorPerson, Verb: task.VerbDismiss}); err != nil {
		t.Fatal(err)
	}
	if got := r.task(t, "t-two"); got.Category != task.CategoryInbox || !slices.Equal(got.Units, []unit.Unit{unitV}) {
		t.Fatalf("t-two %+v", got)
	}
}

func TestDismissTrashesATaskWithNoUnitOfAnEnabledPluginWhole(t *testing.T) {
	r := newRig()
	r.facts.off[unit.GitHub] = true
	r.put(t, task.Task{ID: "t-gh", State: "inbox", Category: task.CategoryInbox, Units: []unit.Unit{unitU},
		Items: []task.ItemRef{{Unit: unitU, Item: "a"}}, Origin: task.OriginUser})
	res, err := r.svc.Do(context.Background(), "t-gh", coretasks.Request{Actor: task.ActorPerson, Verb: task.VerbDismiss})
	got := r.task(t, "t-gh")
	if err != nil || res.Task.Category != task.CategoryDropped || got.Category != task.CategoryDropped || len(got.Units) != 1 || len(got.Items) != 1 {
		t.Fatalf("trashed whole: %+v %+v %v", got, res, err)
	}
}

func TestDismissTrashesADerivedMixedTaskWhole(t *testing.T) {
	r := newRig()
	r.facts.off["slack"] = true
	slack := unit.Unit{Provider: "slack", Key: "C1/1.1"}
	id := task.DerivedID(slack, "i1")
	r.put(t, task.Task{ID: id, State: "next", Category: task.CategoryReady, Units: []unit.Unit{slack, unitU},
		Items: []task.ItemRef{{Unit: slack, Item: "i1"}, {Unit: unitU, Item: "a"}}, Origin: task.OriginDerived})
	if _, err := r.svc.Do(context.Background(), id, coretasks.Request{Actor: task.ActorPerson, Verb: task.VerbDismiss}); err != nil {
		t.Fatal(err)
	}
	if got := r.task(t, id); got.Category != task.CategoryDropped || len(got.Units) != 2 || len(got.Items) != 2 {
		t.Fatalf("a derived task keeps the unit its id names: %+v", got)
	}
}

func TestDetachDropsItemsOnAnyUnitOfTheDisabledPlugin(t *testing.T) {
	r := newRig()
	r.facts.off["slack"] = true
	a, b := unit.Unit{Provider: "slack", Key: "C1/1.1"}, unit.Unit{Provider: "slack", Key: "C1/2.2"}
	r.put(t, task.Task{ID: "t-mixed", State: "inbox", Category: task.CategoryInbox, Units: []unit.Unit{unitU, a},
		Items: []task.ItemRef{{Unit: unitU, Item: "x"}, {Unit: b, Item: "y"}}, Origin: task.OriginUser})
	if _, err := r.svc.Do(context.Background(), "t-mixed", coretasks.Request{Actor: task.ActorPerson, Verb: task.VerbDismiss}); err != nil {
		t.Fatal(err)
	}
	if got := r.task(t, "t-mixed"); !slices.Equal(got.Items, []task.ItemRef{{Unit: unitU, Item: "x"}}) {
		t.Fatalf("items %+v", got.Items)
	}
}

func TestDetachDropsItemsOfADisabledPluginWithNoUnitOnTheTask(t *testing.T) {
	r := newRig()
	r.facts.off["slack"], r.facts.off["jira"] = true, true
	slack, ticket := unit.Unit{Provider: "slack", Key: "C1/1.1"}, unit.Unit{Provider: "jira", Key: "T-1"}
	r.put(t, task.Task{ID: "t-mixed", State: "inbox", Category: task.CategoryInbox, Units: []unit.Unit{unitU, slack},
		Items: []task.ItemRef{{Unit: unitU, Item: "x"}, {Unit: ticket, Item: "y"}}, Origin: task.OriginUser})
	if _, err := r.svc.Do(context.Background(), "t-mixed", coretasks.Request{Actor: task.ActorPerson, Verb: task.VerbDismiss}); err != nil {
		t.Fatal(err)
	}
	if got := r.task(t, "t-mixed"); !slices.Equal(got.Units, []unit.Unit{unitU}) || !slices.Equal(got.Items, []task.ItemRef{{Unit: unitU, Item: "x"}}) {
		t.Fatalf("%+v", got)
	}
}

// trashStates are the bundled states with a verb trash-it whose handler is drop, as a user profile adds one.
type trashStates struct{ coretasks.States }

func (s trashStates) Move(verb string) ([]string, string, bool) {
	if verb == "trash-it" {
		return nil, "trash", true
	}
	return s.States.Move(verb)
}

func (s trashStates) Handler(verb string) string {
	if verb == "trash-it" {
		return coretasks.HandlerDrop
	}
	return s.States.Handler(verb)
}

// TestADropVerbDetachesADisabledPluginsUnitsWhateverItsName checks that a task of a disabled plugin takes
// every verb whose handler is drop, not only the one named dismiss: it trashes a task with no unit of an
// enabled plugin and detaches the disabled plugin's units from a mixed one.
func TestADropVerbDetachesADisabledPluginsUnitsWhateverItsName(t *testing.T) {
	r := newRig()
	off := []unit.ProviderID{"slack"}
	r.svc = coretasks.New(coretasks.Deps{Store: r.store, Items: r.items, Facts: r.facts, Me: me, Clock: r.clock,
		States: trashStates{taskstest.BundledStates}, Disabled: func() []unit.ProviderID { return off }})
	r.facts.off["slack"] = true
	ctx := context.Background()
	slack := unit.Unit{Provider: "slack", Key: "C1/1.1"}
	r.put(t, task.Task{ID: "t-mixed", State: "next", Category: task.CategoryReady, Units: []unit.Unit{unitU, slack}, Origin: task.OriginUser})
	r.put(t, task.Task{ID: "t-slack", State: "inbox", Category: task.CategoryInbox, Units: []unit.Unit{slack}, Origin: task.OriginUser})

	if _, err := r.svc.Do(ctx, "t-mixed", coretasks.Request{Actor: task.ActorPerson, Verb: "trash-it"}); err != nil {
		t.Fatal(err)
	}
	if got := r.task(t, "t-mixed"); got.Category != task.CategoryReady || !slices.Equal(got.Units, []unit.Unit{unitU}) {
		t.Fatalf("detached %+v", got)
	}
	if res, err := r.svc.Do(ctx, "t-slack", coretasks.Request{Actor: task.ActorPerson, Verb: "trash-it"}); err != nil || res.Task.Category != task.CategoryDropped {
		t.Fatalf("trashed: %+v %v", res, err)
	}
}
