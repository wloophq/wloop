package slack

import (
	"testing"

	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestCodecRoundTrips(t *testing.T) {
	reg := unit.NewRegistry(Codec(), unit.OpaqueCodec{ID: unit.GitHub})
	for _, k := range []Key{
		{Team: "T000001", Conversation: "C000001", TS: "1700000000.000100"},
		{Team: "T000001", Conversation: "D000001", Segment: "s1"},
		{Team: "E000001", Conversation: "G000001", Segment: "01HZX-a_b"},
	} {
		u := k.Unit()
		if u.Provider != Provider {
			t.Fatalf("%+v: provider %q", k, u.Provider)
		}
		got, err := ParseUnit(u)
		if err != nil || got != k {
			t.Fatalf("%+v: round trip gave %+v, %v", k, got, err)
		}
		if _, err := reg.Parse(string(Provider), u.Key); err != nil {
			t.Fatalf("%s: registry refused: %v", u.Key, err)
		}
	}
	if k := (Key{Team: "T1", Conversation: "C1", TS: "1.2"}); k.Unit().Key != "T1/C1/1.2" {
		t.Fatalf("thread key %q", k.Unit().Key)
	}
	if k := (Key{Team: "T1", Conversation: "D1", Segment: "x"}); k.Unit().Key != "T1/D1/seg/x" {
		t.Fatalf("segment key %q", k.Unit().Key)
	}

	if _, err := ParseUnit(unit.Unit{Provider: unit.GitHub, Key: "T000001/C000001/1700000000.000100"}); err == nil {
		t.Fatal("a foreign provider was accepted")
	}
	for _, bad := range []string{"", "T1", "T1/C1", "T1/C1/abc", "t1/C1/1.2", "T1/X1/1.2", "T1/C1/1.2/x",
		"T1/D1/seg/", "T1/D1/seg/a b", "T1/D1/sag/x", "owner/repo#1", "T1/C1/1.2\n"} {
		if err := Codec().Valid(bad); err == nil {
			t.Errorf("Valid(%q) accepted", bad)
		}
		if _, err := ParseUnit(unit.Unit{Provider: Provider, Key: bad}); err == nil {
			t.Errorf("ParseUnit(%q) accepted", bad)
		}
	}
	if Codec().Provider() != Provider {
		t.Fatal("codec provider")
	}
}
