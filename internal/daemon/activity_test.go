package daemon

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/turns"
	coreusage "github.com/wloophq/wloop/internal/core/usage"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

func TestActivityAndTimelineLeaveOutTheBookkeeping(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	ctx := context.Background()
	r7 := github.Ref{Owner: "acme", Repo: "api", Number: 7}
	at := s.deps.Now().Add(time.Minute)
	if _, err := s.store().AppendEvents(ctx, []dinv.Event{
		{PR: r7, Kind: dinv.KindSituation, Key: "1", At: at, Data: "review:me"},
		{PR: r7, Kind: coreusage.KindTurnUsage, Key: "1", At: at, Data: "transcript"},
		{PR: r7, Kind: dinv.KindBriefEnded, Key: "1", At: at, Data: "ok"},
		{PR: r7, Kind: dinv.KindUnfollowed, Key: "x", At: at},
		{PR: r7, Kind: turns.KindReconcileReport, Key: "x", At: at},
	}); err != nil {
		t.Fatal(err)
	}
	var items []api.ActivityItem
	get(t, s, "/api/activity", &items)
	var d api.Detail
	get(t, s, "/api/units/github/acme%2Fapi%237", &d)
	shown := slices.Clone(d.Events)
	for _, it := range items {
		shown = append(shown, it.Event)
	}
	for _, e := range shown {
		if dinv.Bookkeeping(e.Kind) {
			t.Errorf("shows the bookkeeping event %s", e.Kind)
		}
	}
	if len(items) != 2 || len(d.Events) != 2 {
		t.Fatalf("activity %+v\ntimeline %+v", items, d.Events)
	}
	for _, kind := range []string{coreusage.KindTurnUsage, turns.KindReconcileReport, dinv.KindFollowed, dinv.KindBriefWanted, dinv.KindBriefGaveUp, workflow.KindPlanned} {
		if !dinv.Bookkeeping(kind) {
			t.Errorf("%s is not bookkeeping", kind)
		}
	}
}

func TestTheTimelineShowsAStepByItsWorkflowWithoutItsPlanning(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	ctx := context.Background()
	r7 := github.Ref{Owner: "acme", Repo: "api", Number: 7}
	at := s.deps.Now().Add(time.Minute)
	if _, err := s.store().AppendEvents(ctx, []dinv.Event{
		{PR: r7, Kind: workflow.KindPlanned, Key: "finished/session/term_1/a:run", At: at},
		{PR: r7, Kind: workflow.KindStepRan, Key: "finished/session/term_1/a:0", Actor: "finished", At: at, Data: "closed term_1"},
	}); err != nil {
		t.Fatal(err)
	}
	var d api.Detail
	get(t, s, "/api/units/github/acme%2Fapi%237", &d)
	var steps []api.Event
	for _, e := range d.Events {
		switch e.Kind {
		case workflow.KindPlanned:
			t.Errorf("the timeline shows %s", e.Kind)
		case workflow.KindStepRan:
			steps = append(steps, e)
		}
	}
	if len(steps) != 1 || steps[0].Actor != "finished" || steps[0].Data != "closed term_1" {
		t.Errorf("steps in the timeline %+v", steps)
	}
}

func TestActivityListsEventsWithTheirPullRequest(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	var items []api.ActivityItem
	if code := get(t, s, "/api/activity?limit=10", &items); code != 200 {
		t.Fatalf("%d", code)
	}
	if len(items) != 2 {
		t.Fatalf("%d items: %+v", len(items), items)
	}
	first := items[0]
	if first.Event.Kind != "review_requested" || first.Title != "Batch export" || first.URL == "" || first.Author != "ana" || first.State != "OPEN" {
		t.Fatalf("%+v", first)
	}
	if first.Mine {
		t.Fatal("ana's pull request marked mine")
	}
	for _, it := range items {
		if it.Event.Unit != it.Event.PR.Unit() {
			t.Fatalf("event of %s names unit %+v", it.Event.PR, it.Event.Unit)
		}
	}
}

func TestActivityLimitIsChecked(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	for _, q := range []string{"limit=0", "limit=1001", "limit=x"} {
		if code := get(t, s, "/api/activity?"+q, nil); code != 400 {
			t.Errorf("%s: %d", q, code)
		}
	}
	var items []api.ActivityItem
	if code := get(t, s, "/api/activity", &items); code != 200 {
		t.Fatalf("default limit: %d", code)
	}
}

func TestActivitySkipsEventsOutsideTheInventoryAndMarksMine(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	ctx := context.Background()
	mine := github.Ref{Owner: "acme", Repo: "api", Number: 9}
	s.store().UpsertPR(ctx, dinv.PR{Ref: mine, Title: "Mine", Author: "Me", State: "OPEN"})
	orphan := github.Ref{Owner: "acme", Repo: "gone", Number: 1}
	if _, err := s.store().AppendEvents(ctx, []dinv.Event{
		{PR: mine, Kind: dinv.KindPush, Key: "m1", Actor: "me", At: s.deps.Now().Add(time.Second)},
		{PR: orphan, Kind: dinv.KindPush, Key: "o1", Actor: "x", At: s.deps.Now().Add(time.Minute)},
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.viewerInfo(ctx); err != nil {
		t.Fatal(err)
	}
	var items []api.ActivityItem
	get(t, s, "/api/activity", &items)
	if len(items) != 3 || github.Ref(items[0].Event.PR) != mine || !items[0].Mine {
		t.Fatalf("%+v", items)
	}
	for _, it := range items {
		if github.Ref(it.Event.PR) == orphan {
			t.Fatal("an event outside the inventory is listed")
		}
	}
}

func TestActivityBudget(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	ctx := context.Background()
	var evs []dinv.Event
	for n := 100; n < 316; n++ {
		ref := github.Ref{Owner: "acme", Repo: "api", Number: n}
		if err := s.store().UpsertPR(ctx, dinv.PR{Ref: ref, Title: "T", Author: "ana", State: "OPEN"}); err != nil {
			t.Fatal(err)
		}
		evs = append(evs, dinv.Event{PR: ref, Kind: dinv.KindPush, Key: "p", Actor: "ana", At: s.deps.Now().Add(time.Duration(n) * time.Second)})
	}
	if _, err := s.store().AppendEvents(ctx, evs); err != nil {
		t.Fatal(err)
	}
	ctx, sc := metertest.Start(t, ctx)
	items, err := s.activity(ctx, activityDefault)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != activityDefault {
		t.Fatalf("%d items", len(items))
	}
	metertest.Within(t, sc, metertest.Budget{{Res: meter.SQL}: 1})
}
