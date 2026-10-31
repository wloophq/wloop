package tasks_test

import (
	"context"
	"slices"
	"testing"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/domain/task"
)

type endedDrafts struct{ ended []string }

func (d *endedDrafts) Ended(_ context.Context, e task.Task, v task.Verb) {
	d.ended = append(d.ended, string(e.ID)+" "+string(v)+" "+string(e.Category))
}

func TestAVerbThatEndsATaskSettlesItsDrafts(t *testing.T) {
	r := newRig()
	drafts := &endedDrafts{}
	r.svc = coretasks.New(coretasks.Deps{Store: r.store, Items: r.items, Facts: r.facts, Me: me, Clock: r.clock, States: taskstest.BundledStates,
		Authority: taskstest.BundledStates.Authority, Wakes: r.wakes, Drafts: drafts})
	for _, c := range []struct {
		id   task.ID
		from string
		verb task.Verb
	}{{"t-done", "inbox", task.VerbDone}, {"t-dismissed", "waiting", task.VerbDismiss}, {"t-filed", "next", task.VerbReference},
		{"t-next", "inbox", task.VerbNext}, {"t-trashed-again", "done", task.VerbDismiss}} {
		e := r.put(t, task.Task{ID: c.id, State: task.State(c.from), Origin: task.OriginUser})
		if _, err := r.svc.Do(context.Background(), e.ID, coretasks.Request{Actor: task.ActorPerson, Verb: c.verb}); err != nil {
			t.Fatalf("%s on %s: %v", c.verb, c.id, err)
		}
	}
	want := []string{"t-done done done", "t-dismissed dismiss dropped", "t-filed reference done"}
	if !slices.Equal(drafts.ended, want) {
		t.Errorf("settled %q, want %q: only a verb that takes an open task to done or dropped settles its drafts", drafts.ended, want)
	}
}

// reentrantDrafts settles a draft as the outbox does: withdrawing it closes its gate, and the gates end a task
// opened only to hold it, back through the tasks service.
type reentrantDrafts struct {
	svc *coretasks.Service
	err error
}

func (d *reentrantDrafts) Ended(ctx context.Context, e task.Task, _ task.Verb) {
	d.err = d.svc.EndHeld(ctx, e.ID, task.CategoryDropped)
}

func TestSettlingTheDraftsOfAnEndedTaskCanCallBackIntoTheTasks(t *testing.T) {
	r := newRig()
	drafts := &reentrantDrafts{}
	r.svc = coretasks.New(coretasks.Deps{Store: r.store, Items: r.items, Facts: r.facts, Me: me, Clock: r.clock, States: taskstest.BundledStates,
		Authority: taskstest.BundledStates.Authority, Wakes: r.wakes, Drafts: drafts})
	drafts.svc = r.svc
	coretasks.SetRaceHooks(r.svc, nil, func() { t.Fatal("settling the drafts waited on the lock the verb still held") })
	e := r.put(t, task.Task{ID: "t-held", State: "inbox", Origin: task.OriginGate})
	if _, err := r.svc.Do(context.Background(), e.ID, coretasks.Request{Actor: task.ActorPerson, Verb: task.VerbDone}); err != nil || drafts.err != nil {
		t.Fatal(err, drafts.err)
	}
}

func TestAVerbItsHandlerEndsSettlesTheTasksDraftsToo(t *testing.T) {
	r := newRig()
	drafts := &endedDrafts{}
	r.svc = coretasks.New(coretasks.Deps{Store: r.store, Items: r.items, Facts: r.facts, Me: me, Clock: r.clock, States: taskstest.BundledStates,
		Authority: taskstest.BundledStates.Authority, Wakes: r.wakes, Drafts: drafts})
	r.svc.Register(coretasks.EveryKind, "finish", func(ctx context.Context, e task.Task, _ coretasks.Request) (coretasks.Result, error) {
		return coretasks.Result{}, r.svc.Close(ctx, e.ID, task.CategoryDone)
	})
	e := r.put(t, task.Task{ID: "t-finished", State: "next", Origin: task.OriginUser})
	if _, err := r.svc.Do(context.Background(), e.ID, coretasks.Request{Actor: task.ActorPerson, Verb: task.VerbDone}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"t-finished done done"}; !slices.Equal(drafts.ended, want) {
		t.Errorf("settled %q, want %q: a verb its handler runs settles the drafts of the task it ended", drafts.ended, want)
	}
}

func TestATaskNoLongerStoredHasEnded(t *testing.T) {
	for _, c := range []struct {
		e    task.Task
		err  error
		want bool
	}{{task.Task{Category: task.CategoryInbox}, nil, false}, {task.Task{Category: task.CategoryDropped}, nil, true},
		{task.Task{}, coretasks.ErrNoTask, true}, {task.Task{}, context.Canceled, false}} {
		if got, err := coretasks.Ended(c.e, c.err); got != c.want || err != nil && c.err == coretasks.ErrNoTask {
			t.Errorf("Ended(%s, %v) = %v, %v; want %v", c.e.Category, c.err, got, err, c.want)
		}
	}
}
