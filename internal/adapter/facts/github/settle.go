package factsgithub

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/port"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/round"
)

func startBlock(ev dinv.Event) (inventory.StartBlock, bool) {
	return inventory.StartBlockOf(port.Event{Unit: ev.PR.Unit(), Kind: ev.Kind, Key: ev.Key, Data: ev.Data, At: ev.At})
}

// Settle writes, after the steps, what planning leaves behind: the
// fingerprint a review stored without its head gets, the first round skip a
// review workspace clears, and the first rounds' block and unblock events.
// st is the sweep's round state; Settle edits it and the sweep saves it.
func (p *Provider) Settle(ctx context.Context, st automation.State) error {
	rs, ok := st.(RoundState)
	if !ok {
		return fmt.Errorf("settling pull requests needs the round store, got %T", st)
	}
	now := p.now()
	v, err := p.build(ctx, now)
	if err != nil {
		return err
	}
	if v.viewer == "" {
		return nil
	}
	v.rounds = rs.Rounds()
	units := v.listed(ctx, now)
	if err := v.loadFacts(ctx, units); err != nil {
		return err
	}
	pl := v.planner(ctx, now)
	everywhere := v.pause.On
	requested := map[pr.Ref]bool{}
	for _, u := range units {
		ref, _ := refOf(u)
		r, err := pl.derive(ref)
		if err != nil {
			return err
		}
		key := ref.String()
		if r.review.upgrade != "" && r.review.settle && !everywhere && !v.pause.Holds(ref.Owner, ref.Repo) {
			v.rounds.SetMark(key, round.Reviewed, r.review.upgrade)
		}
		if v.requested(ref) {
			requested[ref] = true
		}
		s := r.first.settle
		if !s.set {
			continue
		}
		if s.clear {
			v.rounds.ClearFirstRoundSkipped(key)
		}
		if s.unblock != "" {
			if err := p.unblock(ctx, v, ref, s.unblock, now); err != nil {
				return err
			}
		}
		if s.block != "" {
			if err := p.block(ctx, v, ref, s.block, now); err != nil {
				return err
			}
		}
	}
	for ref, ev := range v.blocks {
		if requested[ref] {
			continue
		}
		if _, ok := startBlock(ev); ok && v.stillOpen(ref) {
			continue
		}
		if still, cleared := v.stillRequested(ref); !still {
			if err := p.unblock(ctx, v, ref, cleared, now); err != nil {
				return err
			}
		}
	}
	return nil
}

// requested says whether ref is an open, ready pull request that requests
// the viewer's review by name.
func (v *view) requested(ref pr.Ref) bool {
	row, ok := v.byRef[ref]
	return ok && row.State == dgh.StateOpen && !row.IsDraft && containsFold(row.ReviewRequests, v.viewer)
}

// stillOpen says whether the inventory does not know ref closed.
func (v *view) stillOpen(ref pr.Ref) bool {
	row, found := v.byRef[ref]
	return !found || row.State == dgh.StateOpen
}

// stillRequested tells, from the inventory, whether a blocked pull request
// may still request my review: the inventory may not have read it yet.
func (v *view) stillRequested(ref pr.Ref) (bool, string) {
	row, found := v.byRef[ref]
	switch {
	case !found:
		return true, ""
	case row.State != dgh.StateOpen:
		return false, "it is " + strings.ToLower(row.State)
	case !containsFold(row.ReviewRequests, v.viewer):
		return false, "my review is no longer requested"
	}
	return true, ""
}

// block records why a first round cannot start, unless the inventory says
// so already.
func (p *Provider) block(ctx context.Context, v *view, ref pr.Ref, why string, now time.Time) error {
	if v.blocks[ref].Data == why {
		return nil
	}
	ev := dinv.Event{PR: ref, Kind: dinv.KindRoundBlocked, At: now, Data: why}
	if err := p.d.Store.RecordEvent(ctx, ev); err != nil {
		return fmt.Errorf("%s: recording the blocked first round: %w", ref, err)
	}
	v.blocks[ref] = ev
	return nil
}

// unblock records that a blocked round no longer is.
func (p *Provider) unblock(ctx context.Context, v *view, ref pr.Ref, cleared string, now time.Time) error {
	if _, blocked := v.blocks[ref]; !blocked {
		return nil
	}
	if err := p.d.Store.RecordEvent(ctx, dinv.Event{PR: ref, Kind: dinv.KindRoundUnblocked, At: now, Data: cleared}); err != nil {
		return fmt.Errorf("%s: recording the unblocked round: %w", ref, err)
	}
	delete(v.blocks, ref)
	return nil
}
