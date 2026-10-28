package source

import (
	"context"
	"errors"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

type reporter struct{ b *Broker }

// Report keeps each snapshot of the plugin's own units and the links they
// report; a snapshot of a unit that is not the plugin's, or whose key its
// codec refuses, refuses the whole report.
func (r reporter) Report(ctx context.Context, snapshots []src.Snapshot) error {
	byUnit := make(map[unit.Unit]src.Snapshot, len(snapshots))
	var errs []error
	for _, s := range snapshots {
		u, err := r.b.Units.Own(r.b.ID, s.Unit)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		byUnit[u] = s
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	if err := r.b.Snapshots.Put(ctx, byUnit); err != nil {
		return err
	}
	return r.b.replaceLinks(ctx, byUnit)
}

// Changed records in the change log that the plugin's units changed, as
// unit data and under the host topics behind topics; no unit is a change
// of none in particular. A unit that is not the plugin's refuses it.
func (r reporter) Changed(_ context.Context, units []plugin.Unit, topics []plugin.Topic) error {
	if r.b.Note == nil {
		return src.ErrUnsupported
	}
	own := make([]unit.Unit, 0, len(units))
	var errs []error
	for _, w := range units {
		u, err := r.b.Units.Own(r.b.ID, w)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		own = append(own, u)
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	r.b.Note(port.TopicPRs|HostTopics(topics...), own...)
	return nil
}
