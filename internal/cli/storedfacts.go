package cli

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
	"github.com/wloophq/wloop/internal/turn"
)

// storedFacts reads from the daemon's store what a turn's guide is matched
// against beyond GitHub's read: the requested teams and, when briefs are on,
// the current brief with its tags and the files it read.
type storedFacts struct {
	store  *inventory.Store
	prs    prrow.ReportedPRs
	briefs bool
}

func (s storedFacts) Facts(ctx context.Context, u unit.Unit) (turn.Stored, error) {
	ref, err := pr.ParseKey(u.Key)
	if u.Provider != unit.GitHub || err != nil {
		return s.unitFacts(ctx, u)
	}
	return s.prFacts(ctx, ref)
}

// unitFacts reads the brief in use of a unit the queue does not brief, as
// its provider's job wrote it.
func (s storedFacts) unitFacts(ctx context.Context, u unit.Unit) (turn.Stored, error) {
	var st turn.Stored
	if !s.briefs {
		return st, nil
	}
	ub, ok, err := s.store.UnitBrief(ctx, u)
	if err != nil || !ok {
		return st, err
	}
	b, err := brief.Parse(ub.Brief)
	if err != nil || b == nil {
		return st, err
	}
	st.Brief = &turn.Brief{Goal: b.Goal, Context: b.Context, Kind: b.Kind, Size: b.Size, Risk: b.Risk, RiskWhy: b.RiskWhy,
		OpenQuestions: b.OpenQuestions, Tags: b.Tags, Run: ub.Run, Extra: b.Extra}
	return st, nil
}

func (s storedFacts) prFacts(ctx context.Context, ref pr.Ref) (turn.Stored, error) {
	var st turn.Stored
	pr, known, err := s.prs.PR(ctx, ref)
	if err != nil {
		return st, err
	}
	st.Teams = requestedTeams(pr.ReviewRequests)
	if !s.briefs {
		return st, nil
	}
	rec, ok, err := s.store.BriefRecord(ctx, ref)
	if err != nil || !ok || rec.Brief == "" || rec.Run == 0 {
		return st, err
	}
	var b brief.Brief
	if err := json.Unmarshal([]byte(rec.Brief), &b); err != nil {
		return st, fmt.Errorf("brief run %d: %w", rec.Run, err)
	}
	tags, err := s.store.Tags(ctx, ref)
	if err != nil {
		return st, err
	}
	run, _, err := s.store.BriefRun(ctx, rec.Run)
	if err != nil {
		return st, err
	}
	st.Brief = &turn.Brief{Goal: b.Goal, Context: b.Context, Kind: b.Kind, Size: b.Size, Risk: b.Risk, RiskWhy: b.RiskWhy,
		OpenQuestions: b.OpenQuestions, Tags: tags, Files: run.Files, Run: rec.Run, Extra: b.Extra,
		HeadMoved: known && rec.HeadOid != pr.HeadOid, OlderFormat: rec.Version != brief.Version(brief.DefaultTaxonomy)}
	return st, nil
}

func requestedTeams(requests []string) []string { return pr.RequestedTeams(requests) }
