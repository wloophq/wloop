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
		{Approval{Mode: Always}, true},
		{Approval{Mode: None}, true},
		{Approval{Mode: Observe}, true},
		{Approval{Mode: Window, Window: time.Minute}, true},
		{Approval{Mode: Window}, false},
		{Approval{Mode: Window, Window: -time.Second}, false},
		{Approval{Mode: Always, Window: time.Minute}, false},
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
