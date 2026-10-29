package tasks_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/task"
)

func verbEvents(evs []port.Event) []port.Event {
	return slices.DeleteFunc(slices.Clone(evs), func(ev port.Event) bool { return !strings.HasPrefix(ev.Kind, task.VerbEventKind("")) })
}

func TestAVerbRecordsItsEvent(t *testing.T) {
	r := newRig()
	e := r.put(t, task.Task{ID: "t-x", State: "next", Category: task.CategoryReady, Origin: task.OriginUser})
	r.clock.t = t0.Add(time.Hour)
	if _, err := r.svc.Do(context.Background(), e.ID, coretasks.Request{Actor: task.ActorPerson, Verb: task.VerbDone, Source: "cockpit"}); err != nil {
		t.Fatal(err)
	}
	want := []port.Event{{Unit: task.UnitOf(e.ID), Kind: "verb_done", Actor: "person:cockpit", Data: "next", At: r.clock.t}}
	if got := verbEvents(r.store.Events); !slices.Equal(got, want) {
		t.Errorf("events %+v, want %+v", got, want)
	}
}

func TestARefusedOrSeenVerbRecordsNothing(t *testing.T) {
	r := newRig()
	e := r.put(t, task.Task{ID: "t-x", State: "trash", Category: task.CategoryDropped, Origin: task.OriginUser, Changed: true})
	if _, err := r.svc.Do(context.Background(), e.ID, coretasks.Request{Actor: task.ActorPerson, Verb: task.VerbDone}); !errors.Is(err, coretasks.ErrVerb) {
		t.Fatalf("done on a trashed task: %v", err)
	}
	if _, err := r.svc.Do(context.Background(), e.ID, coretasks.Request{Actor: task.ActorPerson, Verb: task.VerbSeen}); err != nil {
		t.Fatal(err)
	}
	if got := verbEvents(r.store.Events); len(got) > 0 {
		t.Errorf("events %+v", got)
	}
}

func TestAHandledVerbRecordsItsEvent(t *testing.T) {
	r := newProjectRig()
	e := r.put(t, task.Task{ID: "t-x", State: "inbox", Category: task.CategoryInbox, Origin: task.OriginUser, Title: "Draft the API"})
	r.do(t, e.ID, task.VerbProject, map[string]string{"title": "Ship the v2 API"})
	got := verbEvents(r.store.Events)
	if len(got) != 1 || got[0].Kind != "verb_project" || got[0].Unit != task.UnitOf(e.ID) || got[0].Data != "inbox" {
		t.Errorf("events %+v", got)
	}
}

func TestRecordVerbRecordsAVerbAnotherServiceRan(t *testing.T) {
	r := newRig()
	e := r.put(t, task.Task{ID: "t-x", State: "inbox", Category: task.CategoryInbox, Origin: task.OriginUser})
	if err := r.svc.RecordVerb(context.Background(), e, coretasks.Request{Actor: task.ActorPerson, Verb: task.VerbDelegate, Source: "tui"}); err != nil {
		t.Fatal(err)
	}
	want := []port.Event{{Unit: task.UnitOf(e.ID), Kind: "verb_delegate", Actor: "person:tui", Data: "inbox", At: t0}}
	if got := verbEvents(r.store.Events); !slices.Equal(got, want) {
		t.Errorf("events %+v, want %+v", got, want)
	}
}

func TestAVerbThatChangesNothingRecordsNothing(t *testing.T) {
	r := newRig()
	e := r.put(t, task.Task{ID: "t-x", State: "next", Category: task.CategoryReady, Origin: task.OriginUser, Props: task.Props{"area": {Text: "OSS"}}, Written: map[string]task.Written{"area": {By: task.WriterPerson}}})
	if _, err := r.svc.Do(context.Background(), e.ID, coretasks.Request{Actor: task.ActorPerson, Verb: task.VerbArea, Args: map[string]string{"set": "OSS"}}); err != nil {
		t.Fatal(err)
	}
	if got := verbEvents(r.store.Events); len(got) > 0 {
		t.Errorf("events %+v", got)
	}
}
