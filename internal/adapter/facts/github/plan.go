package factsgithub

import (
	"context"
	"maps"
	"time"

	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// Plan is the rounds the facts plan on w over st, the host's round state of
// w: each with the marks the host writes when it sends or skips it.
func (p *Provider) Plan(ctx context.Context, w plugin.Unit, st src.RoundState) (src.Plan, error) {
	u := unit.Unit{Provider: unit.ProviderID(w.Provider), Key: w.Key}
	ref, err := refOf(u)
	if err != nil {
		return src.Plan{}, err
	}
	v, err := p.fresh(ctx, false)
	if err != nil {
		return src.Plan{}, err
	}
	if err := v.loadFacts(ctx, []unit.Unit{u}); err != nil {
		return src.Plan{}, err
	}
	now := p.now()
	pl := v.planner(ctx, now)
	pl.rounds = roundsOf(ref.String(), st, now)
	r, err := pl.derive(ref)
	if err != nil {
		return src.Plan{}, err
	}
	f, _ := v.factsOf(ref)
	head := f.HeadRefOid
	var plan src.Plan
	if r.review.action == actionFollowup {
		fp := r.review.fingerprint
		plan.Rounds = append(plan.Rounds, src.Round{Name: RoundFollowup, Workspace: wsRef(r.review.ws), Head: head, Reason: r.review.reason,
			Observed: r.review.observed, OnSent: kept(map[string]string{round.Reviewed: fp}), OnSkipped: map[string]string{round.Reviewed: fp}})
	}
	if r.first.due {
		plan.Rounds = append(plan.Rounds, src.Round{Name: RoundFirst, Head: head, Reason: r.first.reason,
			OnSkipped: map[string]string{round.FirstSkipped: "1"}})
	}
	if r.address.due {
		marks := kept(map[string]string{round.AddressedReviews: r.address.fingerprint, round.AddressedChecks: r.address.checksHead,
			round.AddressedConflict: r.address.conflictHead})
		plan.Rounds = append(plan.Rounds, src.Round{Name: RoundAddress, Workspace: wsRef(r.address.ws), Head: head, Reason: r.address.reason,
			Observed: r.address.observed, OnSent: marks, OnSkipped: maps.Clone(marks),
			TurnFacts: []src.Fact{
				{Name: "pr.address.reviewers", Value: src.List(r.address.reviewers...)},
				{Name: "pr.address.checks", Value: src.List(r.address.checks...)},
				{Name: "pr.address.conflict", Value: src.Bool(r.address.conflictHead != "")},
			}})
	}
	return plan, nil
}

// kept are the marks of m with a value: a round that covered nothing new
// leaves a mark as it was.
func kept(m map[string]string) map[string]string {
	maps.DeleteFunc(m, func(_, v string) bool { return v == "" })
	return m
}

func wsRef(ws wsdomain.Workspace) src.WorkspaceRef {
	return src.WorkspaceRef{Manager: ws.Manager, ID: ws.ID}
}

// roundsOf is st as the planner reads the round state of key.
func roundsOf(key string, st src.RoundState, now time.Time) *round.Store {
	out := round.New()
	out.PRs[key] = round.PR{Marks: maps.Clone(st.Marks), SentAt: st.SentAt, AgentHome: st.AgentHome}
	out.Days[round.DayKey(now)] = round.Day{Total: st.DayTotal, PerPR: map[string]int{key: st.Today}}
	return out
}

// PlanFacts are the pull request u as the facts read it, which a round they
// plan is sent with.
func (p *Provider) PlanFacts(ctx context.Context, u unit.Unit) (dinv.PlanFacts, error) {
	ref, err := refOf(u)
	if err != nil {
		return dinv.PlanFacts{}, err
	}
	v, err := p.fresh(ctx, false)
	if err != nil {
		return dinv.PlanFacts{}, err
	}
	if err := v.loadFacts(ctx, []unit.Unit{u}); err != nil {
		return dinv.PlanFacts{}, err
	}
	f, _ := v.factsOf(ref)
	return f, nil
}
