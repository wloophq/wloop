package brief

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Projection is how a brief record sets the properties of its task, as
// records.toml declares it: Projects are the fields that set a property,
// by field; Parse is a property's value from a field's text, false when w
// may not write the property or the text is no value of it.
type Projection struct {
	Projects map[string]string
	Parse    func(prop, text string, w task.Writer) (task.Value, bool)
}

// Props are the properties data, a brief record's JSON, sets as w wrote
// it: each projected field that holds a string its property takes.
func (p Projection) Props(data []byte, w task.Writer) map[string]task.Value {
	var fields map[string]json.RawMessage
	if len(p.Projects) == 0 || p.Parse == nil || json.Unmarshal(data, &fields) != nil {
		return nil
	}
	out := map[string]task.Value{}
	for _, field := range slices.Sorted(maps.Keys(p.Projects)) {
		var text string
		if json.Unmarshal(fields[field], &text) != nil || text == "" {
			continue
		}
		if v, ok := p.Parse(p.Projects[field], text, w); ok {
			out[p.Projects[field]] = v
		}
	}
	return out
}

// Records projects brief records onto tasks.
type Records struct {
	// Holders are the tasks that hold a unit.
	Holders interface {
		TasksHolding(ctx context.Context, u unit.Unit) ([]task.ID, error)
	}
	Props interface {
		SetProp(ctx context.Context, id task.ID, name string, v task.Value, w task.Writer) error
	}
	// Projection is the brief record kind's as the profile declares it now.
	Projection func() Projection
}

// ProjectHeld sets, on every task holding u, the properties u's brief
// record data sets, as w wrote it: a pull request task's brief is its pull
// request's.
func (r *Records) ProjectHeld(ctx context.Context, u unit.Unit, data []byte, w task.Writer) error {
	if r == nil {
		return nil
	}
	ids, err := r.Holders.TasksHolding(ctx, u)
	if err != nil {
		return err
	}
	return r.Project(ctx, ids, data, w)
}

// Project sets, on each task of ids, the properties data sets as w wrote
// it; a task gone since is skipped.
func (r *Records) Project(ctx context.Context, ids []task.ID, data []byte, w task.Writer) error {
	if r == nil || r.Projection == nil || len(ids) == 0 {
		return nil
	}
	props := r.Projection().Props(data, w)
	var errs []error
	for _, id := range ids {
		for _, name := range slices.Sorted(maps.Keys(props)) {
			if err := r.Props.SetProp(ctx, id, name, props[name], w); err != nil && !errors.Is(err, coretasks.ErrNoTask) {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}
