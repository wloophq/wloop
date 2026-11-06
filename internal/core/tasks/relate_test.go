package tasks_test

import (
	"context"
	"errors"
	"github.com/wloophq/wloop/internal/core/gates/gatestest"
	"github.com/wloophq/wloop/internal/domain/gate"
	"slices"
	"sync"
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
	applies, events := r.store.Applies, len(r.store.Events)
	res, err := r.svc.Do(ctx, "t-1", relateReq(task.VerbRelate, task.ActorPerson, "t-2"))
	if err != nil {
		t.Fatal(err)
	}
	if r.store.Applies != applies+1 || len(r.store.Events) != events+1 {
		t.Errorf("relate wrote %d applies and %d events, want both sides and its event in one", r.store.Applies-applies, len(r.store.Events)-events)
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

	applies, events = r.store.Applies, len(r.store.Events)
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
	applies, events := r.store.Applies, len(r.store.Events)
	res, err := r.svc.Do(ctx, "t-2", relateReq(task.VerbUnrelate, task.ActorPerson, "t-1"))
	if err != nil {
		t.Fatal(err)
	}
	if r.store.Applies != applies+1 || len(r.store.Events) != events+1 {
		t.Errorf("unrelate wrote %d applies and %d events, want both sides and its event in one", r.store.Applies-applies, len(r.store.Events)-events)
	}
	if ev := r.store.Events[len(r.store.Events)-1]; ev.Unit != task.UnitOf("t-2") {
		t.Errorf("the unrelate's event %+v, want it on t-2", ev)
	} else if other, ok := task.PairOfKey(ev.Key); !ok || other != "t-1" {
		t.Errorf("the unrelate's event keys %q, want the other task t-1", ev.Key)
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
	applies = r.store.Applies
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
	r.put(t, task.Task{ID: "t-d", State: "inbox", Origin: task.OriginDraft})
	r.put(t, task.Task{ID: "t-m", State: "inbox", Origin: task.OriginMessage})
	for _, v := range []task.Verb{task.VerbRelate, task.VerbUnrelate} {
		for _, other := range []task.ID{"t-d", "t-m"} {
			if _, err := r.svc.Do(ctx, "t-1", relateReq(v, task.ActorPerson, other)); !errors.Is(err, coretasks.ErrVerb) {
				t.Errorf("%s with the %s task %s: %v", v, r.task(t, other).Origin, other, err)
			}
		}
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
	if r.store.Applies != 3 {
		t.Errorf("refused relates wrote %d times", r.store.Applies-3)
	}
}

// TestASetDuringARelateIsKept stops a relate between reading its tasks and writing them, and sets a property
// of the other task meanwhile: the relate must hold the service's lock, or it writes its older copy over the set.
func TestASetDuringARelateIsKept(t *testing.T) {
	t.Parallel()
	r := newRig()
	r.put(t, task.Task{ID: "t-1", State: "next", Origin: task.OriginUser})
	r.put(t, task.Task{ID: "t-2", State: "next", Origin: task.OriginUser})
	read, waiting, setDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once, waited sync.Once
	coretasks.SetRaceHooks(r.svc, func() {
		once.Do(func() {
			close(read)
			select {
			case <-waiting:
			case <-setDone:
			}
		})
	}, func() { waited.Do(func() { close(waiting) }) })
	ctx := context.Background()
	var wg sync.WaitGroup
	t.Cleanup(wg.Wait)
	var relateErr, setErr error
	related := make(chan struct{})
	wg.Go(func() {
		defer close(related)
		_, relateErr = r.svc.Do(ctx, "t-1", relateReq(task.VerbRelate, task.ActorPerson, "t-2"))
	})
	select {
	case <-read:
	case <-related:
		t.Fatalf("the relate never stopped between its reads and its write: %v", relateErr)
	}
	wg.Go(func() {
		defer close(setDone)
		setErr = r.svc.SetProp(ctx, "t-2", "priority", task.Value{Text: "high"}, task.WriterWorkflow)
	})
	wg.Wait()
	if relateErr != nil || setErr != nil {
		t.Fatalf("relate %v, set %v", relateErr, setErr)
	}
	if got := r.task(t, "t-2"); got.Prop("priority").Text != "high" || !slices.Equal(got.Prop("related").List, []string{"t-1"}) {
		t.Fatalf("t-2 after both: %+v", got.Props)
	}
	if got := r.related(t, "t-1"); !slices.Equal(got, []string{"t-2"}) {
		t.Fatalf("t-1 related %v", got)
	}
}

func TestAWorkflowsRelateOpensTheRelateGateAndItCannotUnrelate(t *testing.T) {
	ctx := context.Background()
	d := tasksToml(t)
	r := newRig()
	gs := gatestest.NewStore(func() time.Time { return t0 })
	states := profileStates{TasksDef: d, gates: map[string]gate.Gate{"relate": {Name: "relate", Kind: gate.KindProposal, Intents: []string{"approve", "reject"}}}}
	r.svc = coretasks.New(coretasks.Deps{Store: r.store, Items: r.items, Facts: r.facts, Me: me, Clock: r.clock, Gates: gs,
		States: states, Authority: states.Authority})
	r.put(t, task.Task{ID: "t-1", State: "next", Origin: task.OriginUser})
	r.put(t, task.Task{ID: "t-2", State: "next", Origin: task.OriginUser})
	if _, err := r.svc.Do(ctx, "t-1", relateReq(task.VerbRelate, task.ActorPlugin, "t-2")); !errors.Is(err, coretasks.ErrForbidden) {
		t.Errorf("a plugin's relate: %v", err)
	}
	req := relateReq(task.VerbRelate, task.ActorWorkflow, "t-2")
	req.Source = "tidy"
	if _, err := r.svc.Do(ctx, "t-1", req); err != nil {
		t.Fatal(err)
	}
	if got := r.related(t, "t-2"); len(got) != 0 {
		t.Fatalf("t-2 related %v before the gate was decided", got)
	}
	open, _ := gs.OfTask(ctx, "t-1")
	if len(open) != 1 || open[0].Name != "relate" || coretasks.ProposalFrom(open[0]) != "tidy" {
		t.Fatalf("gates %+v, want the relate gate tidy asked for", open)
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
