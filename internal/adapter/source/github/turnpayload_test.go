package ghsource

import (
	"context"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/turn"
)

type onePR struct{ pr turn.PR }

func (o onePR) PR(context.Context, github.Ref) (turn.PR, error) { return o.pr, nil }

func TestTheTurnPayloadIsThePullRequestAtItsUnitAnchoredToTheRecord(t *testing.T) {
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 7}
	at := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	v := &turn.RecordedVerdict{Head: "c3", Endorsed: "yes: by hand", At: at.Add(time.Minute)}
	g := TurnPayloads{PRs: onePR{turn.PR{Title: "Add retries", Prior: &turn.PriorReview{Verdict: "APPROVED", Head: "drifted", At: at}}}}
	r := turn.Request{Unit: ref.Unit(), LastVerdict: v}
	p, err := g.Payload(context.Background(), r)
	pr, _ := p.(*turn.PR)
	if err != nil || pr == nil || pr.URL != ref.URL() || pr.Number != 7 || pr.Prior.Head != "c3" || pr.Prior.Endorsed != "yes: by hand" || p.Unit() != ref.Unit() {
		t.Fatalf("read %+v %v", p, err)
	}
	p, err = g.CompletePayload(r, &turn.PR{Title: "given"})
	if pr, _ := p.(*turn.PR); err != nil || pr == nil || pr.Title != "given" || pr.Slug() != "acme/api#7" {
		t.Fatalf("completed %+v %v", p, err)
	}
	p, err = g.SyntheticPayload(ref.Unit(), true)
	if pr, _ := p.(*turn.PR); err != nil || pr == nil || pr.Title != turn.ProbeTitle || pr.URL != ref.URL() {
		t.Fatalf("a probe's %+v %v", p, err)
	}
}
