package source

import (
	"context"
	"errors"
	"fmt"

	"github.com/wloophq/wloop/internal/brief"
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

// PutBrief keeps a brief the plugin wrote of one of its own units as the
// brief the unit uses, and tells the host's turns waiting on it.
func (r recorder) PutBrief(ctx context.Context, b src.Brief) error {
	if r.b.Briefs == nil || r.b.Now == nil {
		return src.ErrUnsupported
	}
	u, err := r.b.Units.Own(r.b.ID, b.Unit)
	if err != nil {
		return err
	}
	if b.Task != "" {
		if _, err := TaskID(b.Task); err != nil {
			return err
		}
	}
	parsed, err := brief.Parse(string(b.Body))
	if err != nil {
		return fmt.Errorf("the brief of %s is not a brief object: %w", u, err)
	}
	if parsed == nil {
		return fmt.Errorf("the brief of %s is empty", u)
	}
	if err := r.b.Briefs.PutUnitBrief(ctx, u, b.Version, string(b.Body), r.b.Now()); err != nil {
		return err
	}
	if r.b.Briefed != nil {
		r.b.Briefed(u)
	}
	return nil
}
