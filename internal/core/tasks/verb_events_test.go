package tasks_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/task"
)

func verbEvents(evs []port.Event) []port.Event {
	return slices.DeleteFunc(slices.Clone(evs), func(ev port.Event) bool { return !slices.Contains(task.VerbEventKinds, ev.Kind) })
}

func TestAVerbRecordsItsEvent(t *testing.T) {
	r := newRig()
	e := r.put(t, task.Task{ID: "e-x", Kind: task.KindCapture, State: task.Next, Origin: task.OriginUser})
	r.clock.t = t0.Add(time.Hour)
	if _, err := r.svc.Do(context.Background(), e.ID, coretasks.Request{Verb: task.VerbDone, Source: "cockpit"}); err != nil {
		t.Fatal(err)
	}
	want := []port.Event{{Unit: task.UnitOf(e.ID), Kind: "verb_done", Actor: "cockpit", Data: "next", At: r.clock.t}}
	if got := verbEvents(r.store.Events); !slices.Equal(got, want) {
		t.Errorf("events %+v, want %+v", got, want)
	}
}

func TestARefusedOrSeenVerbRecordsNothing(t *testing.T) {
	r := newRig()
	e := r.put(t, task.Task{ID: "e-x", Kind: task.KindCapture, State: task.Trash, Origin: task.OriginUser, Changed: true})
	if _, err := r.svc.Do(context.Background(), e.ID, coretasks.Request{Verb: task.VerbDone}); !errors.Is(err, coretasks.ErrVerb) {
		t.Fatalf("done on a trashed entry: %v", err)
	}
	if _, err := r.svc.Do(context.Background(), e.ID, coretasks.Request{Verb: task.VerbSeen}); err != nil {
		t.Fatal(err)
	}
	if got := verbEvents(r.store.Events); len(got) > 0 {
		t.Errorf("events %+v", got)
	}
}

func TestAHandledVerbRecordsItsEvent(t *testing.T) {
	r := newProjectRig()
	e := r.put(t, task.Task{ID: "e-x", Kind: task.KindCapture, State: task.Inbox, Origin: task.OriginUser, Title: "Draft the API"})
	r.do(t, e.ID, task.VerbProject, map[string]string{"title": "Ship the v2 API"})
	got := verbEvents(r.store.Events)
	if len(got) != 1 || got[0].Kind != "verb_project" || got[0].Unit != task.UnitOf(e.ID) || got[0].Data != "inbox" {
		t.Errorf("events %+v", got)
	}
}

func TestRecordVerbRecordsAVerbAnotherServiceRan(t *testing.T) {
	r := newRig()
	e := r.put(t, task.Task{ID: "e-x", Kind: task.KindAction, State: task.Inbox, Origin: task.OriginUser})
	if err := r.svc.RecordVerb(context.Background(), e, coretasks.Request{Verb: task.VerbDelegate, Source: "tui"}); err != nil {
		t.Fatal(err)
	}
	want := []port.Event{{Unit: task.UnitOf(e.ID), Kind: "verb_delegate", Actor: "tui", Data: "inbox", At: t0}}
	if got := verbEvents(r.store.Events); !slices.Equal(got, want) {
		t.Errorf("events %+v, want %+v", got, want)
	}
}

func TestAVerbThatChangesNothingRecordsNothing(t *testing.T) {
	r := newRig()
	e := r.put(t, task.Task{ID: "e-x", Kind: task.KindCapture, State: task.Next, Origin: task.OriginUser, Area: "OSS"})
	if _, err := r.svc.Do(context.Background(), e.ID, coretasks.Request{Verb: task.VerbArea, Args: map[string]string{"set": "OSS"}}); err != nil {
		t.Fatal(err)
	}
	if got := verbEvents(r.store.Events); len(got) > 0 {
		t.Errorf("events %+v", got)
	}
}

func TestAnAutomatedVerbRecordsNoEvent(t *testing.T) {
	r := newRig()
	e := r.put(t, task.Task{ID: "e-x", Kind: task.KindCapture, State: task.Next, Origin: task.OriginUser})
	res, err := r.svc.Do(context.Background(), e.ID, coretasks.Request{Verb: task.VerbSomeday, Source: "workflow", Automated: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Task.State != task.Someday {
		t.Fatalf("state %s", res.Task.State)
	}
	if err := r.svc.RecordVerb(context.Background(), res.Task, coretasks.Request{Verb: task.VerbDone, Automated: true}); err != nil {
		t.Fatal(err)
	}
	if got := verbEvents(r.store.Events); len(got) > 0 {
		t.Errorf("events %+v", got)
	}
}
