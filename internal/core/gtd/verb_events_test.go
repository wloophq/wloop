package gtd_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/gtd"
)

func verbEvents(evs []port.Event) []port.Event {
	return slices.DeleteFunc(slices.Clone(evs), func(ev port.Event) bool { return !slices.Contains(gtd.VerbEventKinds, ev.Kind) })
}

func TestAVerbRecordsItsEvent(t *testing.T) {
	r := newRig()
	e := r.put(t, gtd.Entry{ID: "e-x", Kind: gtd.KindCapture, State: gtd.Next, Origin: gtd.OriginUser})
	r.clock.t = t0.Add(time.Hour)
	if _, err := r.svc.Do(context.Background(), e.ID, coregtd.Request{Verb: gtd.VerbDone, Source: "cockpit"}); err != nil {
		t.Fatal(err)
	}
	want := []port.Event{{Unit: gtd.UnitOf(e.ID), Kind: "verb_done", Actor: "cockpit", Data: "next", At: r.clock.t}}
	if got := verbEvents(r.store.Events); !slices.Equal(got, want) {
		t.Errorf("events %+v, want %+v", got, want)
	}
}

func TestARefusedOrSeenVerbRecordsNothing(t *testing.T) {
	r := newRig()
	e := r.put(t, gtd.Entry{ID: "e-x", Kind: gtd.KindCapture, State: gtd.Trash, Origin: gtd.OriginUser, Changed: true})
	if _, err := r.svc.Do(context.Background(), e.ID, coregtd.Request{Verb: gtd.VerbDone}); !errors.Is(err, coregtd.ErrVerb) {
		t.Fatalf("done on a trashed entry: %v", err)
	}
	if _, err := r.svc.Do(context.Background(), e.ID, coregtd.Request{Verb: gtd.VerbSeen}); err != nil {
		t.Fatal(err)
	}
	if got := verbEvents(r.store.Events); len(got) > 0 {
		t.Errorf("events %+v", got)
	}
}

func TestAHandledVerbRecordsItsEvent(t *testing.T) {
	r := newProjectRig()
	e := r.put(t, gtd.Entry{ID: "e-x", Kind: gtd.KindCapture, State: gtd.Inbox, Origin: gtd.OriginUser, Title: "Draft the API"})
	r.do(t, e.ID, gtd.VerbProject, map[string]string{"title": "Ship the v2 API"})
	got := verbEvents(r.store.Events)
	if len(got) != 1 || got[0].Kind != "verb_project" || got[0].Unit != gtd.UnitOf(e.ID) || got[0].Data != "inbox" {
		t.Errorf("events %+v", got)
	}
}

func TestRecordVerbRecordsAVerbAnotherServiceRan(t *testing.T) {
	r := newRig()
	e := r.put(t, gtd.Entry{ID: "e-x", Kind: gtd.KindAction, State: gtd.Inbox, Origin: gtd.OriginUser})
	if err := r.svc.RecordVerb(context.Background(), e, coregtd.Request{Verb: gtd.VerbDelegate, Source: "tui"}); err != nil {
		t.Fatal(err)
	}
	want := []port.Event{{Unit: gtd.UnitOf(e.ID), Kind: "verb_delegate", Actor: "tui", Data: "inbox", At: t0}}
	if got := verbEvents(r.store.Events); !slices.Equal(got, want) {
		t.Errorf("events %+v, want %+v", got, want)
	}
}

func TestEveryVerbAStoredEntryTakesHasAnEventKind(t *testing.T) {
	for _, v := range []gtd.Verb{gtd.VerbNext, gtd.VerbWaiting, gtd.VerbSchedule, gtd.VerbSomeday, gtd.VerbReference, gtd.VerbFocus,
		gtd.VerbProject, gtd.VerbApprove, gtd.VerbDelegate, gtd.VerbDone, gtd.VerbDismiss, gtd.VerbContext, gtd.VerbArea,
		gtd.VerbAnswer, gtd.VerbReviewed, gtd.VerbSequential, gtd.VerbReply} {
		if !slices.Contains(gtd.VerbEventKinds, gtd.VerbEventKind(v)) {
			t.Errorf("%s has no event kind", v)
		}
	}
	for _, v := range []gtd.Verb{gtd.VerbSeen, gtd.VerbEdit, gtd.VerbRetry} {
		if slices.Contains(gtd.VerbEventKinds, gtd.VerbEventKind(v)) {
			t.Errorf("%s records an event", v)
		}
	}
}
