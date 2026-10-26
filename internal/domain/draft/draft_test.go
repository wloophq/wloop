package draft

import (
	"errors"
	"testing"
	"time"
)

func TestDraftTransitionsTable(t *testing.T) {
	allowed := map[State][]State{
		Proposed: {Approved, Discarded},
		Approved: {Sending, Discarded},
		Sending:  {Sent, Failed},
		Failed:   {Approved, Discarded},
	}
	for _, from := range States {
		for _, to := range States {
			want := false
			for _, s := range allowed[from] {
				want = want || s == to
			}
			err := Draft{State: from}.Can(to)
			if want && err != nil {
				t.Errorf("%s -> %s refused: %v", from, to, err)
			}
			if !want && !errors.Is(err, ErrTransition) {
				t.Errorf("%s -> %s = %v, want ErrTransition", from, to, err)
			}
		}
	}
	if err := (Draft{State: "bogus"}).Can(Approved); !errors.Is(err, ErrTransition) {
		t.Errorf("unknown state: %v", err)
	}
}

func TestApprovalValidate(t *testing.T) {
	for _, c := range []struct {
		a  Approval
		ok bool
	}{
		{Approval{Mode: Wait}, true},
		{Approval{Mode: Send}, true},
		{Approval{Mode: DryRun}, true},
		{Approval{Mode: Delay, Window: time.Minute}, true},
		{Approval{Mode: Delay}, false},
		{Approval{Mode: Delay, Window: -time.Second}, false},
		{Approval{Mode: Wait, Window: time.Minute}, false},
		{Approval{Mode: "later"}, false},
		{Approval{}, false},
	} {
		if err := c.a.Validate(); (err == nil) != c.ok {
			t.Errorf("%+v: %v, want ok %v", c.a, err, c.ok)
		}
	}
}

func TestKindValid(t *testing.T) {
	for _, k := range Kinds {
		if !k.Valid() {
			t.Errorf("%s invalid", k)
		}
	}
	if Kind("shout").Valid() {
		t.Error("unknown kind valid")
	}
}

func TestShapeAndKindRoundTrip(t *testing.T) {
	t.Parallel()
	want := map[Kind]struct {
		purpose  string
		delivery Delivery
	}{
		Reply: {"reply", Message}, Nudge: {"nudge", Message}, Close: {"close", Message},
		React: {"react", Reaction}, AskOwner: {"ask_owner", Message}, Comment: {"comment", CommentDelivery},
	}
	for _, k := range Kinds {
		p, d := ShapeOf(k)
		if w := want[k]; p != w.purpose || d != w.delivery {
			t.Errorf("%s: %s %s", k, p, d)
		}
		if got := KindOf(p, d); got != k {
			t.Errorf("KindOf(%s, %s) = %s, want %s", p, d, got, k)
		}
	}
	if got := KindOf("close", Reaction); got != React {
		t.Errorf("a closing reaction is a react: %s", got)
	}
	if got := KindOf("ping", Message); got != Reply {
		t.Errorf("a message of any other purpose is a reply: %s", got)
	}
}

func TestStoredSpellingsAreTheLanguages(t *testing.T) {
	for _, c := range [][2]string{
		{string(DryRun), "dry_run"}, {string(WouldWait), "wait"}, {string(WouldRelease), "release"},
		{string(OutcomeReleased), "released"}, {string(OutcomeDryRun), "dry_run"},
	} {
		if c[0] != c[1] {
			t.Errorf("stored %q, want %q", c[0], c[1])
		}
	}
}
