package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/turn"
)

// storedFacts reads from the daemon's store what a turn's guide is matched
// against beyond GitHub's read: the requested teams and, when briefs are on,
// the current brief with its tags and the files it read.
type storedFacts struct {
	store  *inventory.Store
	briefs bool
}

func teamsOf(requests []string) []string {
	var out []string
	for _, r := range requests {
		if t, ok := strings.CutPrefix(r, "team:"); ok {
			out = append(out, strings.Replace(t, "/", "-", 1))
		}
	}
	return out
}

func (s storedFacts) Facts(ctx context.Context, ref github.Ref) (turn.Stored, error) {
	var st turn.Stored
	pr, known, err := s.store.PR(ctx, ref)
	if err != nil {
		return st, err
	}
	st.Teams = teamsOf(pr.ReviewRequests)
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
		OpenQuestions: b.OpenQuestions, Tags: tags, Files: run.Files, Run: rec.Run,
		HeadMoved: known && rec.HeadOid != pr.HeadOid, OlderFormat: rec.Version != brief.Version(brief.DefaultTaxonomy)}
	return st, nil
}
