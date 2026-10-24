package slack

import (
	"testing"

	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestObjects(t *testing.T) {
	if got := ThreadObject("C0001", "1700000000.000100"); got != "C0001/1700000000.000100" {
		t.Errorf("ThreadObject = %q", got)
	}
	for _, c := range []struct{ in, conv, ts string }{
		{"C0001/1700000000.000100", "C0001", "1700000000.000100"},
		{"D0001", "D0001", ""},
	} {
		conv, ts, err := ParseObject(c.in)
		if err != nil || conv != c.conv || ts != c.ts {
			t.Errorf("ParseObject(%q) = %q %q %v", c.in, conv, ts, err)
		}
		ref := ObjectRef{Conversation: conv, TS: ts}
		if ref.Object() != c.in {
			t.Errorf("%+v.Object() = %q", ref, ref.Object())
		}
	}
	for _, bad := range []string{"", "x", "C0001/", "C0001/abc", "D0001/1.2/3"} {
		if _, _, err := ParseObject(bad); err == nil {
			t.Errorf("ParseObject(%q) passed", bad)
		}
	}
	u := ThreadUnit("T0001", "C0001", "1700000000.000100")
	if err := Codec().Valid(u.Key); err != nil || u.Provider != Provider {
		t.Errorf("ThreadUnit = %v %v", u, err)
	}
	reply := Message{TS: "1700000000.000200", ThreadTS: "1700000000.000100"}
	if got := ObjectOf("D0001", KindIM, reply); got != "D0001" {
		t.Errorf("a DM reply belongs to %q", got)
	}
	if got := ObjectOf("C0001", KindChannel, reply); got != "C0001/1700000000.000100" {
		t.Errorf("a channel reply belongs to %q", got)
	}
	if got := ObjectOf("C0001", KindChannel, Message{TS: "1700000000.000300"}); got != "C0001/1700000000.000300" {
		t.Errorf("a lone message belongs to %q", got)
	}
}

func TestVersions(t *testing.T) {
	th := Thread{Conversation: "C0001", TS: "1700000000.000100"}
	if th.Version() != th.TS {
		t.Errorf("a thread without replies: %q", th.Version())
	}
	th.Messages = []Message{{TS: th.TS, EditedTS: "1700000001.000000"}}
	if th.Version() != th.TS+"+1700000001.000000" {
		t.Errorf("an edited lone message: %q", th.Version())
	}
	th.LatestReply = "1700000002.000000"
	if th.Version() != th.LatestReply {
		t.Errorf("a thread with replies: %q", th.Version())
	}
	w := DMWindow{Conversation: "D0001", Latest: "1700000000.000100", Messages: []Message{{TS: "1700000003.000000"}, {TS: "1700000002.000000"}}}
	if w.Version() != "1700000003.000000" {
		t.Errorf("DM version %q", w.Version())
	}
}

func TestCompareTS(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"1700000000.000100", "1700000000.000100", 0},
		{"999999999.900000", "1700000000.000000", -1},
		{"1700000000.1", "1700000000.000100", 1},
		{"", "1.0", -1},
	} {
		if got := CompareTS(c.a, c.b); got != c.want {
			t.Errorf("CompareTS(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestFactsUnits(t *testing.T) {
	seg := Key{Team: "T0001", Conversation: "D0001", Segment: "1700000000000100"}.Unit()
	f := Facts{Team: "T0001",
		Threads: []Thread{{Conversation: "C0002", TS: "1700000000.000100"}, {Conversation: "C0001", TS: "1700000000.000100"}, {Conversation: "C0001", TS: "1700000000.000100"}},
		DMs:     []DMWindow{{Conversation: "D0001", Units: []unit.Unit{seg, seg}}},
	}
	got := f.Units()
	want := []unit.Unit{ThreadUnit("T0001", "C0001", "1700000000.000100"), ThreadUnit("T0001", "C0002", "1700000000.000100"), seg}
	if len(got) != len(want) {
		t.Fatalf("Units = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Units[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}
