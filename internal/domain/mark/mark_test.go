package mark

import (
	"testing"
	"time"
)

func TestAVersionIsHandledOnlyByTheSameValue(t *testing.T) {
	if !Handled("a1b2", "a1b2") || Handled("a1b2", "c3d4") || Handled("", "a1b2") {
		t.Fatal("a version compares by equality, and no mark handles nothing")
	}
}

func TestAWatermarkIsHandledUpToItsPosition(t *testing.T) {
	at := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	stored, ok := Encode(at)
	if !ok {
		t.Fatal("a time encodes")
	}
	for _, c := range []struct {
		current any
		want    bool
	}{
		{at, true},
		{at.Add(-time.Second), true},
		{at.Add(time.Nanosecond), false},
		{time.Time{}, true},
	} {
		if got := Handled(stored, c.current); got != c.want {
			t.Errorf("Handled(%q, %v) = %v", stored, c.current, got)
		}
	}
	if !Handled("2026-10-08T10:00:00Z", at) {
		t.Error("an RFC3339 mark a plugin stored is a watermark too")
	}
	if Handled("a1b2", at) {
		t.Error("a mark that is not a time handles no time")
	}
}

func TestASequenceIsAWatermark(t *testing.T) {
	stored, _ := Encode(42)
	if !Handled(stored, 41) || !Handled(stored, 42) || Handled(stored, 43) {
		t.Fatal("a sequence compares by order")
	}
}

func TestEncodeRefusesWhatCannotBeAMark(t *testing.T) {
	for _, v := range []any{"", time.Time{}, nil, true, []string{"x"}} {
		if s, ok := Encode(v); ok {
			t.Errorf("Encode(%#v) = %q", v, s)
		}
	}
}

func TestAWorkflowMarkLivesUnderItsWorkflow(t *testing.T) {
	if got := Of("address-conflict", "head"); got != "address-conflict/head" {
		t.Fatalf("Of = %q", got)
	}
	for _, c := range []struct {
		name string
		ok   bool
	}{
		{"addressed.checks", true},
		{"reviewed", true},
		{"", false},
		{"a/b", false},
		{"a b", false},
	} {
		if err := ValidName(c.name); (err == nil) != c.ok {
			t.Errorf("ValidName(%q) = %v", c.name, err)
		}
	}
}
