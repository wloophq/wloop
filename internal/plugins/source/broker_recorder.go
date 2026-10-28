package source

import (
	"context"
	"errors"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

type recorder struct{ b *Broker }

// RecordEvents keeps events on the plugin's own units in the host's event
// log; an event of another provider's unit refuses the whole call. The log
// keeps no anchor.
func (r recorder) RecordEvents(ctx context.Context, events []src.Event) error {
	if r.b.Events == nil {
		return src.ErrUnsupported
	}
	evs := make([]port.Event, 0, len(events))
	var errs []error
	for _, e := range events {
		u, err := r.b.Units.Own(r.b.ID, e.Unit)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		evs = append(evs, port.Event{Unit: u, Kind: e.Kind, Key: e.Key, Actor: e.Actor, Data: string(e.Data), At: e.At})
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	return r.b.Events.RecordSourceEvents(ctx, evs...)
}

// SetMarks sets the marks of one of the plugin's own units.
func (r recorder) SetMarks(ctx context.Context, w plugin.Unit, marks map[string]string) error {
	if r.b.Marks == nil {
		return src.ErrUnsupported
	}
	u, err := r.b.Units.Own(r.b.ID, w)
	if err != nil {
		return err
	}
	return r.b.Marks.SetMarks(ctx, u, marks)
}

func (recorder) PutBrief(context.Context, src.Brief) error { return src.ErrUnsupported }
