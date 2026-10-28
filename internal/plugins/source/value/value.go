// Package value converts the values a source plugin reports to the host's:
// a literal, or a workflow.Temporal the host resolves with its clock.
package value

import (
	"time"

	"github.com/wloophq/wloop/internal/domain/workflow"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// Resolve is v's value at now, as a predicate reads it: a temporal value
// resolves with the host's clock, and a zero time resolves to its type's zero.
func Resolve(v src.Value, now time.Time) any {
	f := Fact(v)
	if t, ok := f.(workflow.Temporal); ok {
		return t.Value(now)
	}
	return f
}

// Fact is v as a workflow fact: a literal, or a workflow.Temporal the
// engine resolves with its clock whenever it reads it.
func Fact(v src.Value) any {
	switch v.Kind {
	case src.KindBool:
		return v.Bool
	case src.KindInt:
		return v.Int
	case src.KindString:
		return v.String
	case src.KindList:
		if v.List == nil {
			return []string{}
		}
		return v.List
	case src.KindDuration:
		return v.Duration
	case src.KindTime:
		return v.Time
	case src.KindSince:
		return workflow.Temporal{Kind: workflow.TemporalSince, Time: v.Time}
	case src.KindAt:
		return workflow.Temporal{Kind: workflow.TemporalAt, Time: v.Time}
	case src.KindAfter:
		return workflow.Temporal{Kind: workflow.TemporalAfter, Time: v.Time, Threshold: v.Threshold, WorkingDays: v.Unit == src.WorkingDays}
	}
	return nil
}
