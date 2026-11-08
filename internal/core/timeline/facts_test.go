package timeline_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/timeline"
)

var nine = time.Date(2026, 10, 27, 9, 39, 0, 0, time.UTC)

func ev(seq int64, kind, actor string, at time.Time) timeline.Event {
	return timeline.Event{Seq: seq, Event: port.Event{Kind: kind, Actor: actor, At: at}}
}

func minutes(d time.Duration) string { return fmt.Sprintf("%dm", int(d/time.Minute)) }

var round = timeline.Rule{Name: "round", Start: []string{"turn_started"}, End: []string{"turn_ended"}, Absorb: []string{"situation", "status"},
	Label: "Agent round, {{.duration}}", Running: "Agent round running, {{.count}} events", Tone: "loop"}

func words() timeline.Words {
	return timeline.Words{Kinds: map[string]timeline.Word{"status": {Label: "board", Tone: "me"}}, Duration: minutes}
}

func TestASpanFoldsItsEventsIntoOneNode(t *testing.T) {
	evs := []timeline.Event{
		ev(1, "turn_started", "agent", nine),
		ev(2, "situation", "", nine.Add(2*time.Minute)),
		ev(3, "status", "person", nine.Add(3*time.Minute)),
		ev(4, "turn_ended", "agent", nine.Add(7*time.Minute)),
	}
	got := timeline.Group(evs, []timeline.Rule{round}, words())
	if len(got) != 1 {
		t.Fatalf("%d nodes: %+v", len(got), got)
	}
	n := got[0]
	if n.Rule != "round" || len(n.Events) != 4 || !n.At.Equal(nine) || !n.End.Equal(nine.Add(7*time.Minute)) || n.Label != "Agent round, 7m" || n.Running || n.Tone != "loop" {
		t.Fatalf("%+v", n)
	}
}

func TestASpanWithoutItsEndIsRunning(t *testing.T) {
	evs := []timeline.Event{ev(1, "turn_started", "agent", nine), ev(2, "situation", "", nine.Add(time.Minute))}
	got := timeline.Group(evs, []timeline.Rule{round}, words())
	if len(got) != 1 || !got[0].Running || !got[0].End.IsZero() || got[0].Label != "Agent round running, 2 events" {
		t.Fatalf("%+v", got)
	}
}

func TestARoundThatNeverEndedDoesNotSwallowTheNext(t *testing.T) {
	unended := round
	unended.Unfinished = "Agent round, no end recorded"
	later := nine.Add(72 * time.Hour)
	evs := []timeline.Event{
		ev(1, "turn_started", "agent", nine),
		ev(2, "turn_started", "agent", later),
		ev(3, "turn_ended", "agent", later.Add(7*time.Minute)),
		ev(4, "turn_started", "agent", later.Add(time.Hour)),
		ev(5, "turn_ended", "agent", later.Add(time.Hour+3*time.Minute)),
	}
	got := timeline.Group(evs, []timeline.Rule{unended}, words())
	if len(got) != 3 {
		t.Fatalf("%d nodes, want three rounds: %+v", len(got), got)
	}
	if got[0].Label != "Agent round, 3m" || len(got[0].Events) != 2 || got[1].Label != "Agent round, 7m" || len(got[1].Events) != 2 {
		t.Errorf("the two later rounds read %q (%d events) and %q (%d events)", got[0].Label, len(got[0].Events), got[1].Label, len(got[1].Events))
	}
	if old := got[2]; old.Running || !old.End.IsZero() || old.Label != "Agent round, no end recorded" || len(old.Events) != 1 {
		t.Errorf("the round that never ended %+v, want unfinished, not running, with no end", old)
	}
	unended.Unfinished = ""
	if old := timeline.Group(evs, []timeline.Rule{unended}, words())[2]; old.Label != "turn_started" {
		t.Errorf("without an unfinished text the round reads %q, want its event's words", old.Label)
	}
}

func TestAnUndeclaredKindKeepsItsOwnNode(t *testing.T) {
	evs := []timeline.Event{
		ev(1, "turn_started", "agent", nine),
		ev(2, "turn_ended", "agent", nine.Add(time.Minute)),
		ev(3, "gone_plugin_event", "dana", nine.Add(2*time.Minute)),
		ev(4, "status", "person", nine.Add(3*time.Minute)),
	}
	got := timeline.Group(evs, []timeline.Rule{round}, words())
	if len(got) != 3 {
		t.Fatalf("%d nodes: %+v", len(got), got)
	}
	newest, gone := got[0], got[1]
	if newest.Label != "board" || newest.Tone != "me" || newest.Rule != "" || len(newest.Events) != 1 {
		t.Errorf("a declared kind no rule names: %+v", newest)
	}
	if gone.Label != "gone_plugin_event" || len(gone.Events) != 1 || gone.Events[0].Seq != 3 || gone.Actor != "dana" {
		t.Errorf("an undeclared kind: %+v", gone)
	}
	if got[2].Rule != "round" {
		t.Errorf("oldest last: %+v", got)
	}
}

func TestSingleEventRulesNameTheirActor(t *testing.T) {
	rules := []timeline.Rule{
		{Name: "requested", Start: []string{"review_requested"}, Label: "{{.actor}} requested your review", Tone: "people"},
		{Name: "review", Start: []string{"review"}, Absorb: []string{"comment"}, Label: "{{.actor}} reviewed: {{.sub}}", Subs: map[string]string{"APPROVED": "approved"}},
		{Name: "moved", Start: []string{"status"}, Label: "{{.event}} by {{.actor}}"},
	}
	w := words()
	w.Sub = func(e timeline.Event) string { s, _, _ := strings.Cut(e.Data, ":"); return s }
	evs := []timeline.Event{
		ev(1, "review_requested", "dana", nine),
		{Seq: 2, Event: port.Event{Kind: "review", Actor: "rui", Data: "APPROVED:abc123", At: nine.Add(time.Hour)}},
		ev(3, "comment", "rui", nine.Add(time.Hour)),
		{Seq: 4, Event: port.Event{Kind: "review", Actor: "ana", Data: "DISMISSED:abc123", At: nine.Add(2 * time.Hour)}},
		ev(5, "status", "person", nine.Add(3*time.Hour)),
	}
	got := timeline.Group(evs, rules, w)
	var labels []string
	for _, n := range got {
		labels = append(labels, n.Label)
	}
	want := []string{"board by person", "ana reviewed: DISMISSED", "rui reviewed: approved", "dana requested your review"}
	if strings.Join(labels, "|") != strings.Join(want, "|") {
		t.Fatalf("%q", labels)
	}
	if len(got[2].Events) != 2 || got[3].Tone != "people" || !got[3].End.IsZero() {
		t.Fatalf("a comment folds into the review before it: %+v", got)
	}
}

func BenchmarkGroupATaskOf500Events(b *testing.B) {
	kinds := []string{"turn_started", "situation", "status", "review", "comment", "gone_plugin_event", "turn_ended"}
	evs := make([]timeline.Event, 500)
	for i := range evs {
		evs[i] = ev(int64(i+1), kinds[i%len(kinds)], "dana", nine.Add(time.Duration(i)*time.Minute))
	}
	rules := []timeline.Rule{round, {Name: "review", Start: []string{"review"}, Absorb: []string{"comment"}, Label: "{{.actor}} reviewed"}}
	for b.Loop() {
		timeline.Group(evs, rules, words())
	}
}
