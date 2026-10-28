package overlay

import (
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/brand"
)

var t0 = time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC) // Monday

func h(n int) time.Time { return t0.Add(time.Duration(n) * time.Hour) }

var review = Situation{Kind: "review", WaitingOn: "me", Since: h(1), Reason: "review requested, no review on the head"}

func TestAMuteHoldsUntilItsTime(t *testing.T) {
	m := Mute{Until: h(20), At: h(4)}
	if !m.Holds(review, h(10)) {
		t.Fatal("a mute ahead does not hold")
	}
	if got := m.Muted(); got.Kind != KindMuted || got.WaitingOn != "nobody" || !got.Since.Equal(h(4)) || got.Stalled {
		t.Fatalf("muted %+v", got)
	}
	if (Mute{Until: h(5)}).Holds(review, h(10)) {
		t.Fatal("an expired mute still holds")
	}
	if (Mute{Until: h(20)}).Holds(Situation{Kind: KindDone}, h(10)) {
		t.Fatal("a mute holds a done unit")
	}
	if got := (Mute{Until: h(20)}).Muted(); !got.Since.IsZero() {
		t.Fatalf("a mute with no time dates the muted situation: %+v", got)
	}
}

func TestAMuteUntilTheStateChangesHoldsWhileTheSituationStays(t *testing.T) {
	m := Mute{UntilStateChange: true, Kind: "review", WaitingOn: "me"}
	if !m.Holds(review, h(100)) || m.Lapsed(review) {
		t.Fatal("the muted situation is not held")
	}
	for name, s := range map[string]Situation{
		"kind":       {Kind: "changes", WaitingOn: "author"},
		"waiting on": {Kind: "review", WaitingOn: "carla"},
	} {
		if m.Holds(s, h(10)) || !m.Lapsed(s) {
			t.Errorf("%s changed and the mute still holds", name)
		}
	}
	reordered := Mute{UntilStateChange: true, Kind: "review", WaitingOn: "carla, bruno"}
	if !reordered.Holds(Situation{Kind: "review", WaitingOn: "bruno, carla"}, h(10)) {
		t.Fatal("reordered reviewers unmuted")
	}
}

func TestTheTurnDrivesTheSituation(t *testing.T) {
	for _, c := range []struct {
		name    string
		turn    Turn
		waiting bool
		want    string
		reason  string
	}{
		{"started", Turn{Intent: "review", Role: "pr.review", State: "started", At: h(1)}, false, KindLoopWorking, "review round in progress"},
		{"started on a permission prompt", Turn{Intent: "review", Role: "pr.review", State: "started", At: h(1)}, true, KindLoopWaiting, "waiting for a permission in the terminal"},
		{"asked", Turn{Intent: "review", Role: "pr.review", State: "asked", Summary: "keep the old column?", At: h(2)}, false, KindLoopWaiting, "keep the old column?"},
		{"asked and answered", Turn{Intent: "review", Role: "pr.review", State: "asked", Summary: "s", At: h(2), AnsweredAt: h(3)}, false, KindLoopWorking, "your answer was sent"},
		{"failed", Turn{Intent: "review", Role: "pr.review", State: "failed", Summary: "gh api 502", At: h(2)}, false, KindLoopStopped, "gh api 502"},
		{"unrecorded", Turn{Intent: "review", Role: "pr.review", State: "unrecorded", Summary: "Should I drop it?", At: h(2)}, false, KindLoopStopped, "the agent ended its turn without a record: Should I drop it?"},
		{"unrecorded with no message", Turn{Intent: "address", Role: "pr.address", State: "unrecorded", At: h(2)}, false, KindLoopStopped, "the agent ended its turn without a record"},
	} {
		s, ok := c.turn.Situation(c.waiting)
		if !ok || s.Kind != c.want || s.Reason != c.reason || s.WaitingOn != WaitsOnMe || !s.Since.Equal(c.turn.At) {
			t.Errorf("%s: %+v %v", c.name, s, ok)
		}
	}
	for name, turn := range map[string]Turn{
		"done":                 {Intent: "review", Role: "pr.review", State: "done", Summary: "pushed", At: h(2)},
		"sent":                 {Intent: "review", Role: "pr.review", State: "sent", At: h(2)},
		"a nudge asks nothing": {Intent: "nudge", Role: "pr.nudge", State: "asked", Summary: "s", At: h(2)},
	} {
		if s, ok := turn.Situation(false); ok {
			t.Errorf("%s set the situation: %+v", name, s)
		}
	}
	if s, ok := (Turn{Intent: "address", Role: "pr.address", State: "done", Summary: "pushed", At: h(2)}).Situation(true); ok {
		t.Fatalf("a waiting session overrode %s's ended turn: %+v", brand.Name, s)
	}
}

func TestAPendingRoundWaitsOnTheUserButOnADoneOrMutedUnit(t *testing.T) {
	p := Pending{Intent: "follow-up", Reason: "new commits"}
	got := p.Over(review)
	if got.Kind != KindRoundPending || got.WaitingOn != WaitsOnMe || !got.Since.Equal(review.Since) || got.Reason != "follow-up waiting for your answer: new commits" {
		t.Fatalf("%+v", got)
	}
	for _, s := range []Situation{{Kind: KindDone}, {Kind: KindMuted}} {
		if p.Over(s) != s {
			t.Errorf("a pending round over %s", s.Kind)
		}
	}
}
