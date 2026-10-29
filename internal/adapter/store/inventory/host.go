package inventory

import (
	"context"
	"encoding/json"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// UseHost makes the store write and read the events of its pull requests,
// host data, through h's Recorder and History: the broker the host serves
// the GitHub plugin.
func (s *Store) UseHost(h src.Host) { s.host = h }

// dbHost is the host's event log over the store's own database, which a
// store reaches when no broker is given.
type dbHost struct{ log *sqlite.Store }

func (h dbHost) RecordEvents(ctx context.Context, events []src.Event) error {
	evs := make([]port.Event, len(events))
	for i, e := range events {
		evs[i] = port.Event{Unit: unit.Unit{Provider: unit.ProviderID(e.Unit.Provider), Key: e.Unit.Key}, Kind: e.Kind, Key: e.Key,
			Actor: e.Actor, Data: string(e.Data), At: e.At}
	}
	return h.log.RecordSourceEvents(ctx, evs...)
}

func (dbHost) SetMarks(context.Context, plugin.Unit, map[string]string) error {
	return src.ErrUnsupported
}

func (dbHost) PutBrief(context.Context, src.Brief) error { return src.ErrUnsupported }

func (h dbHost) Events(ctx context.Context, q src.EventQuery) ([]src.Event, error) {
	units := make([]unit.Unit, len(q.Units))
	for i, u := range q.Units {
		units[i] = unit.Unit{Provider: unit.ProviderID(u.Provider), Key: u.Key}
	}
	evs, err := h.log.UnitEvents(ctx, units, q.Kinds, q.Since)
	if err != nil {
		return nil, err
	}
	out := make([]src.Event, len(evs))
	for i, e := range evs {
		out[i] = src.Event{Unit: plugin.Unit{Provider: string(e.Unit.Provider), Key: e.Unit.Key}, Kind: e.Kind, Key: e.Key, Actor: e.Actor, At: e.At}
		if e.Data != "" {
			out[i].Data = json.RawMessage(e.Data)
		}
	}
	return out, nil
}

// Host is the broker the store records its events through.
func (s *Store) Host() src.Host { return s.host }
