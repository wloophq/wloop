package source

import (
	"slices"
	"time"
)

// Fact is one workflow fact of a unit.
type Fact struct {
	Name  string
	Value Value
}

// ValueKind is what a [Value] holds.
type ValueKind int

const (
	KindNone ValueKind = iota
	KindBool
	KindInt
	KindString
	KindList
	KindDuration
	KindTime
	// KindSince resolves to the duration from Time to the host's now.
	KindSince
	// KindAt resolves to true once the host's now reaches Time, a deadline.
	KindAt
	// KindAfter resolves to true once the time from Time to the host's now,
	// counted in Unit, exceeds Threshold; that instant is a deadline.
	KindAfter
)

// TimeUnit is how an After value counts time.
type TimeUnit int

const (
	Calendar TimeUnit = iota
	// WorkingDays counts Monday to Friday only, in UTC.
	WorkingDays
)

// Value is a fact's value: a literal, or a temporal value the host resolves
// with its clock whenever it reads it. Build one with its constructor. A
// temporal value whose Time is zero resolves to its type's zero (a zero
// duration, false) and has no deadline.
type Value struct {
	Kind      ValueKind
	Bool      bool
	Int       int64
	String    string
	List      []string
	Duration  time.Duration
	Time      time.Time
	Threshold time.Duration
	Unit      TimeUnit
}

func Bool(b bool) Value              { return Value{Kind: KindBool, Bool: b} }
func Int(i int64) Value              { return Value{Kind: KindInt, Int: i} }
func String(s string) Value          { return Value{Kind: KindString, String: s} }
func List(s ...string) Value         { return Value{Kind: KindList, List: slices.Clone(s)} }
func Duration(d time.Duration) Value { return Value{Kind: KindDuration, Duration: d} }
func Time(t time.Time) Value         { return Value{Kind: KindTime, Time: t} }

// Since is the age of t: a duration fact.
func Since(t time.Time) Value { return Value{Kind: KindSince, Time: t} }

// At is true from t on: a bool fact whose deadline is t.
func At(t time.Time) Value { return Value{Kind: KindAt, Time: t} }

// After is true once more than threshold, counted in unit, has passed since
// t: a bool fact.
func After(t time.Time, threshold time.Duration, unit TimeUnit) Value {
	return Value{Kind: KindAfter, Time: t, Threshold: threshold, Unit: unit}
}

// Temporal says whether v changes with the host's clock.
func (v Value) Temporal() bool { return v.Kind >= KindSince }
