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

func (d *endedDrafts) Ended(_ context.Context, e task.Task, v task.Verb) error {
	d.ended = append(d.ended, string(e.ID)+" "+string(v)+" "+string(e.Category))
	return nil
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
