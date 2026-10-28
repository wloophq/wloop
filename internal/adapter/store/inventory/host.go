package inventory

import (
	"context"
	"encoding/json"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/port"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
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

// wireEvent is e as the broker carries it.
func wireEvent(e dinv.Event) src.Event {
	u := e.PR.Unit()
	out := src.Event{Unit: plugin.Unit{Provider: string(u.Provider), Key: u.Key}, Kind: e.Kind, Key: e.Key, Actor: e.Actor, At: e.At}
	if e.Data != "" {
		out.Data = json.RawMessage(e.Data)
	}
	return out
}

// recordEvents records evs on the host through the broker.
func (s *Store) recordEvents(ctx context.Context, evs []dinv.Event) error {
	if len(evs) == 0 {
		return nil
	}
	if err := timed(evs); err != nil {
		return err
	}
	wire := make([]src.Event, len(evs))
	for i, e := range evs {
		wire[i] = wireEvent(e)
	}
	return s.host.RecordEvents(ctx, wire)
}

// hostEvents reads the host's events of refs of kinds, every kind when
// empty, through the broker.
func (s *Store) hostEvents(ctx context.Context, refs []github.Ref, kinds ...string) ([]dinv.Event, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	units := make([]plugin.Unit, len(refs))
	for i, r := range refs {
		u := r.Unit()
		units[i] = plugin.Unit{Provider: string(u.Provider), Key: u.Key}
	}
	got, err := s.host.Events(ctx, src.EventQuery{Units: units, Kinds: kinds})
	if err != nil {
		return nil, err
	}
	out := make([]dinv.Event, len(got))
	for i, e := range got {
		out[i] = dinv.Event{PR: refOf(e.Unit.Provider, e.Unit.Key), Kind: e.Kind, Key: e.Key, Actor: e.Actor, At: e.At, Data: string(e.Data)}
	}
	return out, nil
}
