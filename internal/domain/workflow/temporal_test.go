package workflow

import (
	"testing"
	"time"
)

func TestTemporalFactsResolveWithTheClock(t *testing.T) {
	fri := time.Date(2026, 10, 2, 20, 0, 0, 0, time.UTC)
	mon := time.Date(2026, 10, 5, 3, 0, 0, 0, time.UTC)
	after := Temporal{Kind: TemporalAfter, Time: fri, Threshold: 6 * time.Hour, WorkingDays: true}
	if after.Value(mon) != true || after.Value(mon.Add(-time.Nanosecond)) != true || after.Value(time.Date(2026, 10, 5, 2, 0, 0, 0, time.UTC)) != false {
		t.Errorf("after in working days: %v", after.Value(mon))
	}
	if at, ok := after.Deadline(fri); !ok || !at.Equal(time.Date(2026, 10, 5, 2, 0, 0, 1, time.UTC)) || after.Value(at) != true || after.Value(at.Add(-1)) != false {
		t.Errorf("after's deadline %v %t", at, ok)
	}
	if _, ok := after.Deadline(mon); ok {
		t.Error("a deadline that passed is still one")
	}
	for _, zero := range []Temporal{{Kind: TemporalSince}, {Kind: TemporalAt}, {Kind: TemporalAfter, Threshold: time.Hour}} {
		if _, ok := zero.Deadline(mon); ok {
			t.Errorf("%+v has a deadline", zero)
		}
		if v := zero.Value(mon); v != time.Duration(0) && v != false {
			t.Errorf("%+v resolved to %v", zero, v)
		}
	}
	f := Facts{"a.since": Temporal{Kind: TemporalSince, Time: fri}, "a.at": Temporal{Kind: TemporalAt, Time: mon}, "a.after": after, "a.n": 1}
	got, due := Resolve(f, fri.Add(time.Hour))
	if got["a.since"] != time.Hour || got["a.at"] != false || got["a.after"] != false || got["a.n"] != 1 || !due.Equal(time.Date(2026, 10, 5, 2, 0, 0, 1, time.UTC)) {
		t.Errorf("resolved %v, due %v", got, due)
	}
	if _, ok := f["a.since"].(Temporal); !ok {
		t.Error("Resolve wrote its input")
	}
}
