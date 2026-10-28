package ghsource

import (
	"context"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/turn"
)

type onePayload struct{ p turn.Payload }

func (o onePayload) Payload(context.Context, turn.Request) (turn.Payload, error) { return o.p, nil }

func TestTheTurnPayloadIsThePluginsAndAHandedOneIsAnchoredToTheRecord(t *testing.T) {
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 7}
	at := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	v := &turn.RecordedVerdict{Head: "c3", Endorsed: "yes: by hand", At: at.Add(time.Minute)}
	plugin := &turn.PR{Title: "from the plugin"}
	g := TurnPayloads{Plugin: onePayload{plugin}}
	r := turn.Request{Unit: ref.Unit(), LastVerdict: v}
	if p, err := g.Payload(context.Background(), r); err != nil || p != plugin {
		t.Fatalf("read %+v %v", p, err)
	}
	p, err := g.CompletePayload(r, &turn.PR{Title: "given", Prior: &turn.PriorReview{Verdict: "APPROVED", Head: "drifted", At: at}})
	pr, _ := p.(*turn.PR)
	if err != nil || pr == nil || pr.Title != "given" || pr.Slug() != "acme/api#7" || pr.URL != ref.URL() || pr.Prior.Head != "c3" || pr.Prior.Endorsed != "yes: by hand" {
		t.Fatalf("completed %+v %v", p, err)
	}
	p, err = g.SyntheticPayload(ref.Unit(), true)
	if pr, _ := p.(*turn.PR); err != nil || pr == nil || pr.Title != turn.ProbeTitle || pr.URL != ref.URL() {
		t.Fatalf("a probe's %+v %v", p, err)
	}
}
