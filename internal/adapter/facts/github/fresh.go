package factsgithub

import (
	"context"
	"time"

	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
)

// Moved reads, in one batched read before the loop lock, the stub of each
// unit's pull request and returns those GitHub shows closed or moved since
// the inventory row the plan read: under the lock a round asks GitHub
// nothing. A failed read moves them all. Units are planned again after the
// next fast run. A unit whose rounds are all observed is not read, as no
// round of it would go out.
func (p *Provider) Moved(ctx context.Context, units []unit.Unit) ([]unit.Unit, error) {
	if p.d.Stubs == nil {
		return nil, nil
	}
	v, err := p.fresh(ctx, false)
	if err != nil {
		return nil, err
	}
	if v.partial {
		return nil, nil
	}
	if err := v.loadFacts(ctx, units); err != nil {
		return nil, err
	}
	pl := v.planner(ctx, p.now())
	var refs []pr.Ref
	for _, u := range units {
		ref, err := refOf(u)
		if err != nil {
			continue
		}
		if r, err := pl.derive(ref); err != nil || !r.runs() {
			continue
		}
		refs = append(refs, ref)
	}
	if len(refs) == 0 {
		return nil, nil
	}
	stubs, errs, err := p.d.Stubs.StubsByRef(ctx, refs)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	var moved []unit.Unit
	for _, ref := range refs {
		if err != nil || movedFrom(v.byRef, ref, stubs[ref], errs[ref]) {
			moved = append(moved, ref.Unit())
		}
	}
	if len(moved) > 0 {
		p.dueAt(dueMoved, p.now().Add(time.Duration(v.cfg.Sweep.Fast)))
	}
	return moved, nil
}

// movedFrom says whether stub, GitHub's pull request of ref now, no longer
// stands for the stored row the plan read.
func movedFrom(rows map[pr.Ref]dinv.PR, ref pr.Ref, stub github.Stub, readErr error) bool {
	if stubGone(stub, readErr) {
		return true
	}
	row, ok := rows[ref]
	return ok && (stub.HeadOid != row.HeadOid || stub.State != row.State || !stub.UpdatedAt.Equal(row.UpdatedAt))
}

// stubGone says GitHub returned no open pull request for the stub.
func stubGone(stub github.Stub, readErr error) bool {
	return readErr != nil || stub.Ref.Number == 0 || stub.State != dgh.StateOpen
}
