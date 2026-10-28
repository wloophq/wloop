package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// waitsForYou holds a gate for a person: folder trust is the user's alone,
// so no approval rule releases a start block's gate.
type waitsForYou struct{}

func (waitsForYou) Decide(context.Context, gate.Gate) (gates.Decision, error) {
	return gates.Decision{Mode: gate.ModeWait}, nil
}

// syncTrustGates opens a trust gate on each unit a start block holds the
// round of and that has none open, its subject the block, and supersedes
// each open trust gate of a unit no block holds any more.
func (e *Env) syncTrustGates(ctx context.Context) error {
	if e.gates == nil || e.gateReader == nil || e.Rec == nil {
		return nil
	}
	blocks, err := e.Rec.FirstRoundBlocks(ctx)
	if err != nil {
		return err
	}
	open, err := e.gateReader.Where(ctx, gates.Query{Kind: gate.KindTrust})
	if err != nil {
		return err
	}
	gated := map[unit.Unit]bool{}
	for _, g := range open {
		gated[g.Unit] = true
	}
	blocked := map[unit.Unit]bool{}
	var units []unit.Unit
	for u, ev := range blocks {
		if _, ok := inventory.StartBlockOf(ev); ok {
			blocked[u] = true
			units = append(units, u)
		}
	}
	slices.SortFunc(units, func(a, b unit.Unit) int {
		return cmp.Or(cmp.Compare(a.Provider, b.Provider), cmp.Compare(a.Key, b.Key))
	})
	var errs []error
	for _, u := range units {
		if gated[u] {
			continue
		}
		if _, err := e.gates.Open(ctx, gate.Gate{Kind: gate.KindTrust, Unit: u, Subject: blocks[u].Key}, waitsForYou{}); err != nil {
			errs = append(errs, fmt.Errorf("opening the trust gate of %s: %w", u.Label(), err))
		}
	}
	for _, g := range open {
		if blocked[g.Unit] {
			continue
		}
		if err := e.gates.Supersede(ctx, g.ID, task.ActorWorkflow); err != nil && !errors.Is(err, gates.ErrResolved) {
			errs = append(errs, fmt.Errorf("closing the trust gate of %s: %w", g.Unit.Label(), err))
		}
	}
	return errors.Join(errs...)
}
