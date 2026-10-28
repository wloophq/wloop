package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

type recorder struct{ b *Broker }

// RecordEvents keeps events on the plugin's own units in the host's event
// log, each with its anchor; an event of another provider's unit refuses
// the whole call.
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
		evs = append(evs, port.Event{Unit: u, Kind: e.Kind, Key: e.Key, Actor: e.Actor, Data: string(e.Data), At: e.At, Anchor: anchorOf(e.Anchor)})
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

// PutBrief keeps a brief the plugin wrote of one of its own units. With a
// task, which must hold the unit or one of its items, it is that task's brief
// record. Without one it is the brief the unit uses, and each task holding
// an item of the unit gets, as its brief record, the brief with the asks of
// the items it holds. Either way the turns waiting on the unit's brief are
// told.
func (r recorder) PutBrief(ctx context.Context, b src.Brief) error {
	if r.b.Briefs == nil || r.b.Now == nil {
		return src.ErrUnsupported
	}
	u, err := r.b.Units.Own(r.b.ID, b.Unit)
	if err != nil {
		return err
	}
	var id task.ID
	if b.Task != "" {
		if id, err = TaskID(b.Task); err != nil {
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
	held, err := r.b.Briefs.ItemsHeld(ctx, u)
	if err != nil {
		return err
	}
	now := r.b.Now()
	if id != "" {
		if _, ok := held[id]; !ok {
			return fmt.Errorf("the task %s holds neither %s nor any of its items", id, u)
		}
		err := r.putTaskBrief(ctx, id, b.Body, now)
		if err == nil && r.b.Briefed != nil {
			r.b.Briefed(u)
		}
		return err
	}
	if err := r.b.Briefs.PutUnitBrief(ctx, u, b.Version, string(b.Body), now); err != nil {
		return err
	}
	var errs []error
	for _, id := range slices.Sorted(maps.Keys(held)) {
		body, ok, err := asksOf(b.Body, held[id])
		if err != nil {
			return fmt.Errorf("the brief of %s: %w", u, err)
		}
		if ok {
			errs = append(errs, r.putTaskBrief(ctx, id, body, now))
		}
	}
	if r.b.Briefed != nil {
		r.b.Briefed(u)
	}
	return errors.Join(errs...)
}

func (r recorder) putTaskBrief(ctx context.Context, id task.ID, body []byte, at time.Time) error {
	if err := r.b.Briefs.PutRecord(ctx, task.UnitOf(id), builtin.BuildsBrief, task.WriterPlugin, body, at); err != nil {
		return err
	}
	return r.b.BriefRecords.Project(ctx, []task.ID{id}, body, task.WriterPlugin)
}

// asksOf is body with only the asks about items; false when none of its
// asks is about one of them.
func asksOf(body json.RawMessage, items []string) (json.RawMessage, bool, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, false, err
	}
	var asks []map[string]json.RawMessage
	if raw, ok := fields[asksField]; !ok || json.Unmarshal(raw, &asks) != nil {
		return nil, false, nil
	}
	kept := asks[:0]
	for _, a := range asks {
		var item string
		if json.Unmarshal(a[askItemField], &item) == nil && slices.Contains(items, item) {
			kept = append(kept, a)
		}
	}
	if len(kept) == 0 {
		return nil, false, nil
	}
	raw, err := json.Marshal(kept)
	if err != nil {
		return nil, false, err
	}
	fields[asksField] = raw
	out, err := json.Marshal(fields)
	return out, err == nil, err
}

// The fields of the brief record's schema that split a conversation's brief
// per task: its asks, each about the item it names.
const (
	asksField    = "asks"
	askItemField = "item"
)
