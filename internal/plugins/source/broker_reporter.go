package source

import (
	"context"
	"errors"

	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

type reporter struct{ b *Broker }

// Report keeps each snapshot of the plugin's own units; a snapshot of a
// unit that is not the plugin's, or whose key its codec refuses, refuses
// the whole report.
func (r reporter) Report(_ context.Context, snapshots []src.Snapshot) error {
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
	r.b.Snapshots.Put(byUnit)
	return nil
}

func (r reporter) Changed(context.Context, []plugin.Unit, []plugin.Topic) error {
	return src.ErrUnsupported
}
