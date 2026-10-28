package timeline

import (
	"context"
	"slices"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// KindRecord is the event kind of a record: its Key is the record kind of
// records.toml, its Actor the writer, its Data the record's JSON. A unit
// keeps one record of each kind, the newest.
const KindRecord = "record"

// Log reads the event log.
type Log interface {
	// EventsOf are the events of units f keeps, newest first (by Seq).
	EventsOf(ctx context.Context, units []unit.Unit, f Filter) ([]Event, error)
}

// Holdings are the units the tasks hold.
type Holdings interface {
	// UnitsOf are the units task id holds, in its order; empty for a task
	// with none or no such task.
	UnitsOf(ctx context.Context, id task.ID) ([]unit.Unit, error)
}

// Merged is the Reader over the event log: a task's timeline is its own
// events and those of the units it holds.
type Merged struct {
	Log      Log
	Holdings Holdings
}

var _ Reader = Merged{}

func (m Merged) Of(ctx context.Context, id task.ID, f Filter) ([]Event, error) {
	held, err := m.Holdings.UnitsOf(ctx, id)
	if err != nil {
		return nil, err
	}
	units := []unit.Unit{task.UnitOf(id)}
	for _, u := range held {
		if !slices.Contains(units, u) {
			units = append(units, u)
		}
	}
	evs, err := m.Log.EventsOf(ctx, units, f)
	if evs == nil {
		evs = []Event{}
	}
	return evs, err
}

// Records reads the records the units keep.
type Records interface {
	// Records are the record of kind on each of units that keeps one.
	Records(ctx context.Context, kind string, units ...unit.Unit) (map[unit.Unit]port.Event, error)
}

// RecordOf is task id's record of kind: its own, else the first of held,
// the units it holds in order, that keeps one, as a pull request task's
// brief is its pull request's; false when none does.
func RecordOf(ctx context.Context, r Records, kind string, id task.ID, held []unit.Unit) (port.Event, bool, error) {
	units := append([]unit.Unit{task.UnitOf(id)}, held...)
	got, err := r.Records(ctx, kind, units...)
	if err != nil {
		return port.Event{}, false, err
	}
	for _, u := range units {
		if ev, ok := got[u]; ok {
			return ev, true, nil
		}
	}
	return port.Event{}, false, nil
}
