package round

import (
	"testing"
	"time"
)

func TestCapAndFingerprint(t *testing.T) {
	now := time.Date(2026, 10, 9, 10, 0, 0, 0, time.Local)
	s := New()
	if s.CapReason("a#1", now, 1) != "" {
		t.Fatal("capped on a fresh state")
	}
	s.RecordSent("a#1", "reviewed", "fp1", now)
	if s.CapReason("a#1", now, 1) == "" {
		t.Fatal("per-PR cap not applied")
	}
	if s.CapReason("a#2", now, 1) != "" {
		t.Fatal("another PR capped by the per-PR cap")
	}
	if !s.FirstCapHit("a#3", now) || s.FirstCapHit("a#3", now) {
		t.Fatal("cap hit should be reported once per day")
	}
	if s.Mark("a#1", "reviewed") != "fp1" || s.Mark("a#1", "addressed.reviews") != "" {
		t.Fatalf("marks: reviewed %q, addressed %q", s.Mark("a#1", "reviewed"), s.Mark("a#1", "addressed.reviews"))
	}
	if s.CapReason("a#1", now.Add(24*time.Hour), 1) != "" {
		t.Fatal("cap did not reset the next day")
	}
}

func TestSettersRecordWhatTheyChanged(t *testing.T) {
	now := time.Date(2026, 10, 9, 10, 0, 0, 0, time.Local)
	s := New()
	s.SetMark("a/b#1", "addressed.checks", "abc")
	s.RecordSent("a/b#1", "addressed.reviews", "", now)
	s.FirstNote("hello", now)
	s.FirstNote("hello", now)
	ch := s.Changes()
	if got, want := ch.PRs["a/b#1"], FieldSentAt; got != want {
		t.Fatalf("fields %b, want %b", got, want)
	}
	if got := ch.Marks["a/b#1"]; len(got) != 1 || !got["addressed.checks"] {
		t.Fatalf("marks %v, want only addressed.checks", got)
	}
	if _, ok := ch.PRs["a/b#2"]; ok {
		t.Fatal("a clear that changed nothing was recorded")
	}
	if len(ch.Counts) != 1 || ch.Counts[0] != (Count{Day: DayKey(now), Key: "a/b#1"}) {
		t.Fatalf("counts %+v", ch.Counts)
	}
	if len(ch.Notes) != 1 || ch.Notes[0].Kind != NoteText {
		t.Fatalf("notes %+v", ch.Notes)
	}
	s.Saved()
	if ch := s.Changes(); len(ch.PRs)+len(ch.Marks)+len(ch.Counts)+len(ch.Notes) != 0 {
		t.Fatalf("changes kept after Saved: %+v", ch)
	}
}

func TestPauseHoldsEverythingOrItsRepositories(t *testing.T) {
	p := Pause{Repos: []string{"acme/sandbox"}}
	if !p.Holds("acme", "Sandbox") || p.Holds("acme", "api") {
		t.Fatalf("Holds: sandbox %v, api %v", p.Holds("acme", "sandbox"), p.Holds("acme", "api"))
	}
	if p.On = true; !p.Holds("acme", "api") {
		t.Fatal("an on pause leaves a repository acting")
	}
	if (Pause{}).Holds("acme", "api") {
		t.Fatal("an off pause holds")
	}
}

func TestAnEmptyMarkIsNoMark(t *testing.T) {
	s := New()
	s.SetMark("a/b#1", "addressed.reviews", "2026-10-09T10:00:00Z")
	s.SetMark("a/b#1", "addressed.reviews", "")
	if _, ok := s.PRs["a/b#1"].Marks["addressed.reviews"]; ok {
		t.Fatal("an emptied mark is kept")
	}
	if !s.Changes().Marks["a/b#1"]["addressed.reviews"] {
		t.Fatal("removing a mark is a change to save")
	}
}
