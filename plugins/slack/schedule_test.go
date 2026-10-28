package slack

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	dslack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/items"
	"github.com/wloophq/wloop/plugins/slack/unit"
)

type fakeHeat struct {
	heat map[unit.Unit]Heat
	err  error
}

func (h fakeHeat) Heat(context.Context, []unit.Unit) (map[unit.Unit]Heat, error) {
	return h.heat, h.err
}

// lone seeds lone channel messages at ts(n), long before f.now.
func (f *fixture) lone(t *testing.T, ns ...int) []string {
	t.Helper()
	var objects []string
	for _, n := range ns {
		f.seed(t, dslack.Facts{Threads: []dslack.Thread{{Conversation: chan1, TS: ts(n), Messages: []dslack.Message{{TS: ts(n), User: other, Text: "x"}}}}})
		objects = append(objects, dslack.ThreadObject(chan1, ts(n)))
	}
	return objects
}

func TestScheduleFollowsHeat(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	objects := f.lone(t, 1, 2, 3)
	recent := ts(int(f.now.Add(-time.Hour).Unix() - 1700000000))
	f.seed(t, dslack.Facts{Threads: []dslack.Thread{{Conversation: chan1, TS: recent, Messages: []dslack.Message{{TS: recent, User: other, Text: "x"}}}}})
	objects = append(objects, dslack.ThreadObject(chan1, recent), dslack.ThreadObject(chan1, ts(9)))
	heat := fakeHeat{heat: map[unit.Unit]Heat{
		dslack.ThreadUnit(team, chan1, ts(1)):  HeatActive,
		dslack.ThreadUnit(team, chan1, ts(2)):  HeatWarm,
		dslack.ThreadUnit(team, chan1, ts(3)):  HeatCold,
		dslack.ThreadUnit(team, chan1, recent): HeatCold,
	}}
	next, err := HeatScheduler{DB: f.db.Slack(), Heat: heat}.Next(context.Background(), objects, f.now)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []time.Duration{5 * time.Minute, 30 * time.Minute, 60 * time.Minute, 5 * time.Minute, 5 * time.Minute} {
		if got := next[objects[i]].Sub(f.now); got != want {
			t.Errorf("%s: next in %v, want %v", objects[i], got, want)
		}
	}
}

func TestScheduleSurvivesHeatErrors(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	objects := f.lone(t, 1, 2)
	next, err := HeatScheduler{DB: f.db.Slack(), Heat: fakeHeat{err: errors.New("items table locked")}, Log: f.logs.logf}.
		Next(context.Background(), objects, f.now)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range objects {
		if got := next[o].Sub(f.now); got != PollActive {
			t.Fatalf("%s: next in %v", o, got)
		}
	}
	if !strings.Contains(f.logs.String(), "items table locked") {
		t.Fatalf("the heat error was not logged: %s", f.logs)
	}
}

func TestColdObjectWaitsAnHour(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	objects := f.lone(t, 1, 2)
	open := dslack.ThreadUnit(team, chan1, ts(2))
	kind, summary := items.Request, "ask"
	if err := f.host.Apply(items.User, items.Op{Op: items.OpCreate, Unit: open, Item: "i1",
		Anchor: unit.Anchor{Unit: open, Ref: chan1 + "/" + ts(2)}, Fields: &items.Fields{Kind: &kind, Summary: &summary}}); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(time.Minute)
	if err := f.p.src.store.See(context.Background(), objects, f.now); err != nil {
		t.Fatal(err)
	}
	for i, poll := range []time.Duration{PollCold, PollActive} {
		o, _ := f.object(t, objects[i])
		if want := f.now.Add(poll); !o.NextCheckAt.Equal(want) {
			t.Fatalf("%s: next check %v, want %v", objects[i], o.NextCheckAt, want)
		}
	}
	stubs, err := f.p.src.store.Watched(context.Background())
	if err != nil || len(stubs) != 0 {
		t.Fatalf("due right after the pass: %v %v", stubs, err)
	}
}
