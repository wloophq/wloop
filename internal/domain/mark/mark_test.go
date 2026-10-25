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
	v, ok := New(at)
	if !ok || !v.Watermark {
		t.Fatal("a time is a watermark")
	}
	stored := v.Value
	for _, c := range []struct {
		current any
		want    bool
	}{
		{at, true},
		{at.Add(-time.Second), true},
		{at.Add(time.Nanosecond), false},
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
	v, _ := New(42)
	stored := v.Value
	if !Handled(stored, 41) || !Handled(stored, 42) || Handled(stored, 43) {
		t.Fatal("a sequence compares by order")
	}
}

func TestNewRefusesWhatCannotBeAMark(t *testing.T) {
	for _, v := range []any{"", time.Time{}, nil, true, []string{"x"}} {
		if m, ok := New(v); ok {
			t.Errorf("New(%#v) = %+v", v, m)
		}
	}
	if v, _ := New("h1"); v.Watermark {
		t.Error("a string is a version")
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

func TestAnEmptySignalIsHandled(t *testing.T) {
	for _, current := range []any{"", time.Time{}} {
		if !Handled("", current) {
			t.Errorf("Handled(no mark, %#v) = false; nothing to handle is handled", current)
		}
	}
}

func TestAWatermarkNeverMovesBack(t *testing.T) {
	at := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	later, _ := New(at.Add(time.Hour))
	earlier, _ := New(at)
	if got := Merge(later.Value, earlier); got != later.Value {
		t.Errorf("an earlier time replaced %q with %q", later.Value, got)
	}
	if got := Merge(earlier.Value, later); got != later.Value {
		t.Errorf("a later time kept %q", got)
	}
	nine, _ := New(9)
	if got := Merge("10", nine); got != "10" {
		t.Errorf("9 replaced 10: %q", got)
	}
	v, _ := New("h2")
	if got := Merge("h1", v); got != "h2" {
		t.Errorf("a version is replaced: %q", got)
	}
	if got := Merge("", nine); got != "9" {
		t.Errorf("no mark takes the value: %q", got)
	}
}
