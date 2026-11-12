package outbox

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// specs are draft kinds by provider and name.
type specs []draft.KindSpec

func (k specs) Lookup(provider, kind string) (draft.KindSpec, bool) {
	for _, s := range k {
		if s.Provider == provider && s.Name == kind {
			return s, true
		}
	}
	return draft.KindSpec{}, false
}

func (specs) Validate(draft.KindSpec, json.RawMessage) error { return nil }

// sendsOn holds when facts[name] is "COMMENT".
type sendsOn struct{ name string }

func (c sendsOn) Holds(facts map[string]any, _ time.Time) (bool, error) {
	return facts[c.name] == "COMMENT", nil
}

func TestDecideTakesTheKindSpecOnlyFromTheManifests(t *testing.T) {
	const fact = "draft.forge.review.event"
	pol := &draft.Policy{Default: waiting, Rules: []draft.Rule{{When: sendsOn{fact}, Approval: draft.Approval{Mode: draft.Send}, Names: "review", Payload: []string{fact}}}}
	bogus := draft.KindSpec{Provider: "forge", Name: "review", Facts: []draft.FactSpec{{Name: fact, Pointer: "/body"}}}
	manifest := draft.KindSpec{Provider: "forge", Name: "review", Delivery: "review", Facts: []draft.FactSpec{{Name: fact, Pointer: "/event"}}}
	d := draft.Draft{Unit: unit.Unit{Provider: "forge", Key: "acme/api#7"}, Kind: "review", Delivery: "review", Payload: json.RawMessage(`{"event":"APPROVE","body":"COMMENT"}`)}
	p := Proposal{Policy: pol, Subject: &draft.Subject{Kind: bogus}}

	for name, s := range map[string]*Service{
		"no manifests":      {},
		"manifests":         {Kinds: specs{manifest}},
		"manifests of none": {Kinds: specs{}},
	} {
		dec, _ := s.decide(context.Background(), p, d, time.Time{})
		if dec.Approval.Releases() {
			t.Errorf("%s: the proposer's kind spec read agent text as a trusted fact: %+v", name, dec)
		}
	}
	d.Payload = json.RawMessage(`{"event":"COMMENT"}`)
	s := &Service{Kinds: specs{manifest}}
	if dec, _ := s.decide(context.Background(), p, d, time.Time{}); !dec.Approval.Releases() {
		t.Errorf("the manifest's kind spec gives no payload fact: %+v", dec)
	}
}
