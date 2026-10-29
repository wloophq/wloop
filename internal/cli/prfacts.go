package cli

import (
	"context"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/turn"
)

// prFacts reads, in the daemon, what a pull request's guide is matched
// against: GitHub's read of it, else the inventory's, and the stored facts.
func (e *Env) prFacts(cfg config.Config) func(ctx context.Context, u unit.Unit) (api.Facts, error) {
	return func(ctx context.Context, u unit.Unit) (api.Facts, error) {
		ref := prOf(u)
		f := api.Facts{Owner: ref.Owner, Repo: ref.Repo}
		if read, err := e.forgeBasics(ctx, ref); err == nil {
			p := turnPR(read)
			f.Author, f.Association, f.Base, f.Labels = p.Author, p.Association, p.Base, p.Labels
		} else {
			p, ok, serr := e.reportedPRs().PR(ctx, ref)
			if serr != nil {
				return f, serr
			}
			if !ok {
				return f, err
			}
			f.Author, f.Base = p.Author, p.BaseBranch
		}
		if e.Store == nil {
			return f, nil
		}
		st, err := storedFacts{store: e.Store, prs: e.reportedPRs(), briefs: cfg.Brief.Enabled}.Facts(ctx, ref.Unit())
		if err != nil {
			return f, err
		}
		f.Teams = st.Teams
		if b := st.Brief; b != nil {
			f.Kind, f.Size, f.Risk, f.Tags, f.Files, f.HasBrief = b.Kind, b.Size, b.Risk, b.Tags, b.Files, true
			f.Goal, f.Context, f.RiskWhy = b.Goal, b.Context, b.RiskWhy
		}
		return f, nil
	}
}

// factsTurn reads a turn's stored facts from the daemon's answer, for a
// command line process, which has no store.
type factsTurn struct{ f api.Facts }

func (t factsTurn) Facts(context.Context, unit.Unit) (turn.Stored, error) {
	st := turn.Stored{Teams: t.f.Teams}
	if t.f.HasBrief {
		st.Brief = &turn.Brief{Goal: t.f.Goal, Context: t.f.Context, Kind: t.f.Kind, Size: t.f.Size, Risk: t.f.Risk, RiskWhy: t.f.RiskWhy, Tags: t.f.Tags, Files: t.f.Files}
	}
	return st, nil
}
