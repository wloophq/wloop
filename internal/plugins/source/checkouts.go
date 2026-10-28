package source

import (
	"context"

	"github.com/wloophq/wloop/internal/domain/unit"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// Checkouts is a source plugin's Lander as the host reads where a unit's
// work checks out, which directory holds a unit, and a task's move written
// back to the source.
type Checkouts struct {
	ID    string
	Units Units
	Kind  *Kind
}

// Checkout is the repository u's work checks out.
func (c Checkouts) Checkout(ctx context.Context, u unit.Unit) (src.Checkout, bool, error) {
	cl, err := c.Kind.Client(c.ID)
	if err != nil {
		return src.Checkout{}, false, err
	}
	return cl.Checkout(ctx, Wire(u))
}

// UnitAt is the unit the checkout at dir holds, as unit.Locator says it.
func (c Checkouts) UnitAt(ctx context.Context, dir string) (string, bool, error) {
	cl, err := c.Kind.Client(c.ID)
	if err != nil {
		return "", false, err
	}
	u, ok, err := cl.Locate(ctx, dir)
	if err != nil || !ok {
		return "", ok, err
	}
	got, err := c.Units.Own(c.ID, u)
	return got.Key, err == nil, err
}

// WriteState writes a task's move back to the source.
func (c Checkouts) WriteState(ctx context.Context, u unit.Unit, task, state, category string) (src.Evidence, error) {
	cl, err := c.Kind.Client(c.ID)
	if err != nil {
		return src.Evidence{}, err
	}
	return cl.WriteState(ctx, src.StateWrite{Unit: Wire(u), Task: task, State: state, Category: category})
}

var _ unit.Locator = Checkouts{}
