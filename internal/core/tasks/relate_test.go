package tasks_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/task"
)

func relateReq(v task.Verb, a task.Actor, other task.ID) coretasks.Request {
	return coretasks.Request{Verb: v, Actor: a, Source: "cockpit", Args: map[string]string{task.ArgTask: string(other)}}
}

func (r *rig) related(t *testing.T, id task.ID) []string {
	t.Helper()
	return r.task(t, id).Prop("related").List
}

func TestRelateLinksBothTasksAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	r := newRig()
	r.put(t, task.Task{ID: "t-1", State: "next", Origin: task.OriginUser, Title: "Draft the launch post"})
	r.put(t, task.Task{ID: "t-2", State: "inbox", Origin: task.OriginUser, Title: "Book the launch venue"})
	r.put(t, task.Task{ID: "t-3", State: "done", Origin: task.OriginUser})
	later := t0.Add(time.Hour)
	r.clock.t = later
	res, err := r.svc.Do(ctx, "t-1", relateReq(task.VerbRelate, task.ActorPerson, "t-2"))
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Task.Prop("related").List; res.Task.ID != "t-1" || !slices.Equal(got, []string{"t-2"}) {
		t.Errorf("the result %s related %v", res.Task.ID, got)
	}
	if got := r.related(t, "t-1"); !slices.Equal(got, []string{"t-2"}) {
		t.Errorf("t-1 related %v", got)
	}
	if got := r.related(t, "t-2"); !slices.Equal(got, []string{"t-1"}) {
		t.Errorf("t-2 related %v", got)
	}
	if e := r.task(t, "t-2"); e.State != "inbox" || e.Written["related"].By != task.WriterWorkflow || !e.Updated.Equal(later) {
		t.Errorf("t-2 %s, related written by %q, updated %v", e.State, e.Written["related"].By, e.Updated)
	}
	if evs := verbEvents(r.store.Events); len(evs) != 1 || evs[0].Unit != task.UnitOf("t-1") || evs[0].Actor != "person:cockpit" {
		t.Errorf("events %+v, want one relate on t-1", evs)
	}

	applies, events := r.store.Applies, len(r.store.Events)
	for _, again := range []struct{ on, other task.ID }{{"t-1", "t-2"}, {"t-2", "t-1"}} {
		if _, err := r.svc.Do(ctx, again.on, relateReq(task.VerbRelate, task.ActorPerson, again.other)); err != nil {
			t.Fatal(err)
		}
	}
	if r.store.Applies != applies || len(r.store.Events) != events {
		t.Errorf("relating related tasks wrote: %d applies, %d events", r.store.Applies-applies, len(r.store.Events)-events)
	}

	if _, err := r.svc.Do(ctx, "t-3", relateReq(task.VerbRelate, task.ActorPerson, "t-1")); err != nil {
		t.Fatal(err)
	}
	if got := r.related(t, "t-1"); !slices.Equal(got, []string{"t-2", "t-3"}) {
		t.Errorf("t-1 related %v after a closed task related it", got)
	}
	if got := r.related(t, "t-3"); !slices.Equal(got, []string{"t-1"}) {
		t.Errorf("t-3 related %v", got)
	}
}

func TestUnrelateTakesTheLinkOffBoth(t *testing.T) {
	ctx := context.Background()
	r := newRig()
	for _, id := range []task.ID{"t-1", "t-2", "t-3"} {
		r.put(t, task.Task{ID: id, State: "next", Origin: task.OriginUser})
	}
	for _, other := range []task.ID{"t-2", "t-3"} {
		if _, err := r.svc.Do(ctx, "t-1", relateReq(task.VerbRelate, task.ActorPerson, other)); err != nil {
			t.Fatal(err)
		}
	}
	res, err := r.svc.Do(ctx, "t-2", relateReq(task.VerbUnrelate, task.ActorPerson, "t-1"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Task.ID != "t-2" || len(res.Task.Prop("related").List) != 0 {
		t.Errorf("the result %s related %v", res.Task.ID, res.Task.Prop("related").List)
	}
	if got := r.related(t, "t-1"); !slices.Equal(got, []string{"t-3"}) {
		t.Errorf("t-1 related %v, want t-3 only", got)
	}
	if _, ok := r.task(t, "t-2").Props["related"]; ok {
		t.Errorf("t-2 keeps a related property: %v", r.related(t, "t-2"))
	}
	if got := r.related(t, "t-3"); !slices.Equal(got, []string{"t-1"}) {
		t.Errorf("t-3 related %v", got)
	}
	applies := r.store.Applies
	if _, err := r.svc.Do(ctx, "t-2", relateReq(task.VerbUnrelate, task.ActorPerson, "t-1")); !errors.Is(err, coretasks.ErrArgs) {
		t.Errorf("unrelating tasks no longer related: %v", err)
	}
	if r.store.Applies != applies {
		t.Errorf("a refused unrelate wrote")
	}
}

func TestRelateRefusesATaskWithItself(t *testing.T) {
	ctx := context.Background()
	r := newRig()
	r.put(t, task.Task{ID: "t-1", State: "next", Origin: task.OriginUser})
	for _, v := range []task.Verb{task.VerbRelate, task.VerbUnrelate} {
		if _, err := r.svc.Do(ctx, "t-1", relateReq(v, task.ActorPerson, "t-1")); !errors.Is(err, coretasks.ErrArgs) {
			t.Errorf("%s with itself: %v", v, err)
		}
		if _, err := r.svc.Do(ctx, "t-1", coretasks.Request{Verb: v, Actor: task.ActorPerson}); !errors.Is(err, coretasks.ErrArgs) {
			t.Errorf("%s naming no task: %v", v, err)
		}
		if _, err := r.svc.Do(ctx, "t-1", relateReq(v, task.ActorPerson, "t-404")); !errors.Is(err, coretasks.ErrNoTask) {
			t.Errorf("%s with no such task: %v", v, err)
		}
	}
	if got := r.related(t, "t-1"); len(got) != 0 {
		t.Errorf("t-1 related %v", got)
	}
	if r.store.Applies != 1 {
		t.Errorf("refused relates wrote %d times", r.store.Applies-1)
	}
}

func TestAWorkflowCannotRelate(t *testing.T) {
	ctx := context.Background()
	r := profileRig(t, tasksToml(t))
	r.put(t, task.Task{ID: "t-1", State: "next", Origin: task.OriginUser})
	r.put(t, task.Task{ID: "t-2", State: "next", Origin: task.OriginUser})
	for _, a := range []task.Actor{task.ActorWorkflow, task.ActorPlugin} {
		if _, err := r.svc.Do(ctx, "t-1", relateReq(task.VerbRelate, a, "t-2")); !errors.Is(err, coretasks.ErrForbidden) {
			t.Errorf("a %s's relate: %v", a, err)
		}
	}
	if got := r.related(t, "t-2"); len(got) != 0 {
		t.Fatalf("t-2 related %v after refused relates", got)
	}
	if _, err := r.svc.Do(ctx, "t-1", relateReq(task.VerbRelate, task.ActorPerson, "t-2")); err != nil {
		t.Fatal(err)
	}
	for _, a := range []task.Actor{task.ActorWorkflow, task.ActorPlugin} {
		if _, err := r.svc.Do(ctx, "t-1", relateReq(task.VerbUnrelate, a, "t-2")); !errors.Is(err, coretasks.ErrForbidden) {
			t.Errorf("a %s's unrelate: %v", a, err)
		}
	}
	if got := r.related(t, "t-2"); !slices.Equal(got, []string{"t-1"}) {
		t.Errorf("t-2 related %v after refused unrelates", got)
	}
}
