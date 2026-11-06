package source

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/proposals"
	"github.com/wloophq/wloop/internal/domain/draft"
)

// Proposals are a source plugin's Planner.Proposals as the proposals job
// reads them: drafts of the plugin's own rules on its own units, each with
// an auto origin naming its rule, and no delivery of the plugin's, and the
// withdrawals of its rules on its own units.
type Proposals struct {
	ID    string
	Units Units
	Kind  *Kind
}

func (p Proposals) Proposals(ctx context.Context, now time.Time) (proposals.Pass, error) {
	c, err := p.Kind.Client(p.ID)
	if err != nil {
		return proposals.Pass{}, err
	}
	got, err := c.Proposals(ctx, now)
	if err != nil {
		return proposals.Pass{}, err
	}
	out := proposals.Pass{Proposals: make([]outbox.Proposal, 0, len(got.Drafts))}
	var errs []error
	for _, w := range got.Withdrawals {
		u, err := p.Units.Own(p.ID, w.Unit)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out.Withdrawals = append(out.Withdrawals, proposals.Withdrawal{Unit: u, Rule: w.Rule})
	}
	for _, w := range got.Drafts {
		u, err := p.Units.Own(p.ID, w.Unit)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if w.Origin.Kind != string(draft.OriginAuto) || w.Origin.Rule == "" {
			errs = append(errs, fmt.Errorf("plugin %s proposed a draft on %s that names no rule as an auto origin", p.ID, u))
			continue
		}
		if _, err := TaskID(w.Task); err != nil {
			errs = append(errs, err)
			continue
		}
		pr := outbox.Proposal{Unit: u, Items: w.Items, Task: w.Task, Kind: draft.Kind(w.Kind), Target: w.Target, Text: w.Text, Reaction: w.Reaction,
			Purpose: w.Purpose, Effects: w.Effects, Origin: draft.Origin{Kind: draft.OriginAuto, Rule: w.Origin.Rule, Key: w.Origin.Key}}
		if pr.Kind != "" {
			pr.Purpose = ""
		}
		out.Proposals = append(out.Proposals, pr)
	}
	return out, errors.Join(errs...)
}
