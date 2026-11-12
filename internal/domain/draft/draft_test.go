package draft

import (
	"errors"
	"testing"
	"time"
)

func TestDraftTransitionsTable(t *testing.T) {
	allowed := map[State][]State{
		Held:    {Queued, Failed, Dropped},
		Queued:  {Sending, Failed, Dropped},
		Sending: {Sent, Failed},
		Failed:  {Queued, Dropped},
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
	if err := (Draft{State: "bogus"}).Can(Queued); !errors.Is(err, ErrTransition) {
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

func TestStoredSpellingsAreTheLanguages(t *testing.T) {
	for _, c := range [][2]string{
		{string(DryRun), "dry_run"}, {string(Held), "held"}, {string(Queued), "queued"}, {string(Dropped), "dropped"},
	} {
		if c[0] != c[1] {
			t.Errorf("stored %q, want %q", c[0], c[1])
		}
	}
}
