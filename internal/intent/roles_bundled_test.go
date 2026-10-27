package intent_test

import (
	"testing"
	"time"

	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/turn"
)

// TestRolesImplyTheRemovedKeys pins what each bundled intent's role and
// workspace imply to what its manifest declared before the keys went.
func TestRolesImplyTheRemovedKeys(t *testing.T) {
	l, err := turn.Bundled()
	if err != nil {
		t.Fatal(err)
	}
	type implied struct {
		roundKind, reconcile, prepare, context, launch, policy, board string
		drivesRow                                                     bool
	}
	want := map[string]implied{
		"act":     {context: "pr", policy: "round", board: "none"},
		"address": {roundKind: "address", context: "pr", policy: "round", board: "author", drivesRow: true},
		"brief":   {context: "pr", policy: "round", board: "none"},
		"nudge":   {prepare: "nudge.prepare", context: "pr", policy: "round", board: "none"},
		"probe":   {context: "synthetic", launch: "probe", policy: "probe", board: "none"},
		"review":  {roundKind: "review", reconcile: "my_review", context: "pr", policy: "round", board: "review", drivesRow: true},
	}
	for _, d := range l.Registry.All() {
		got := implied{d.RoundKind, d.Reconcile, d.Prepare, d.Context, d.Launch, d.Policy, d.Board, d.DrivesRow}
		if got != want[string(d.Name)] {
			t.Errorf("%s: %+v, want %+v", d.Name, got, want[string(d.Name)])
		}
	}
}

func TestWhyRendersTheReason(t *testing.T) {
	l, err := turn.Bundled()
	if err != nil {
		t.Fatal(err)
	}
	facts := map[string]any{"pr.review.reason": "the author answered", "pr.address.reason": "reviewed by bob"}
	for name, want := range map[string]string{"review": "the author answered", "address": "reviewed by bob", "nudge": ""} {
		if got, err := l.Why(dintent.Name(name), facts); err != nil || got != want {
			t.Errorf("%s: %q %v, want %q", name, got, err, want)
		}
	}
}

func TestTheBundledNudgeRefusesWithItsFirstHoldingRefuse(t *testing.T) {
	l, err := turn.Bundled()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		facts map[string]any
		want  string
	}{
		{map[string]any{"pr.nudge.refusal": "it waits on you", "pr.mute.refusal": "muted"}, "it waits on you"},
		{map[string]any{"pr.nudge.refusal": "", "pr.mute.refusal": "muted"}, "muted"},
		{map[string]any{}, ""},
	} {
		if got, err := l.Refusal("nudge", c.facts); err != nil || got != c.want {
			t.Errorf("%v: %q %v, want %q", c.facts, got, err, c.want)
		}
	}
	if got, _ := l.Refusal("review", map[string]any{"pr.mute.refusal": "muted"}); got != "" {
		t.Errorf("the review, with no [[refuse]], refused: %q", got)
	}
}

func TestTheBundledSessionNames(t *testing.T) {
	l, err := turn.Bundled()
	if err != nil {
		t.Fatal(err)
	}
	facts := map[string]any{"unit.label": "acme/api#7", "session.started": time.Date(2026, 10, 26, 14, 5, 0, 0, time.UTC)}
	for name, want := range map[string]string{
		"review":  "review acme/api#7 26/10 14:05",
		"address": "triage acme/api#7 26/10 14:05",
		"nudge":   "nudge acme/api#7",
	} {
		if got, err := l.SessionName(dintent.Name(name), facts); err != nil || got != want {
			t.Errorf("%s: %q %v, want %q", name, got, err, want)
		}
	}
}
