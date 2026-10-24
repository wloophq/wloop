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
	s.RecordFollowup("a#1", "fp1", now)
	if s.CapReason("a#1", now, 1) == "" {
		t.Fatal("per-PR cap not applied")
	}
	if s.CapReason("a#2", now, 1) != "" {
		t.Fatal("another PR capped by the per-PR cap")
	}
	if !s.FirstCapHit("a#3", now) || s.FirstCapHit("a#3", now) {
		t.Fatal("cap hit should be reported once per day")
	}
	if s.Fingerprint("a#1") != "fp1" {
		t.Fatalf("fingerprint lost: %q", s.Fingerprint("a#1"))
	}
	if s.CapReason("a#1", now.Add(24*time.Hour), 1) != "" {
		t.Fatal("cap did not reset the next day")
	}
}

func TestSettersRecordWhatTheyChanged(t *testing.T) {
	now := time.Date(2026, 10, 9, 10, 0, 0, 0, time.Local)
	s := New()
	s.SetChecksHead("a/b#1", "abc")
	s.RecordFollowup("a/b#1", "", now)
	s.ClearFirstRoundSkipped("a/b#2")
	s.FirstNote("hello", now)
	s.FirstNote("hello", now)
	ch := s.Changes()
	if got, want := ch.PRs["a/b#1"], FieldChecksHead|FieldSentAt|FieldRoundAt; got != want {
		t.Fatalf("fields %b, want %b", got, want)
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
	if ch := s.Changes(); len(ch.PRs)+len(ch.Counts)+len(ch.Notes) != 0 {
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
