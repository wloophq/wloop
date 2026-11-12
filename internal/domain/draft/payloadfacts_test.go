package draft

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
)

const reviewEvent = "draft.forge.review.event"

var reviewSpec = KindSpec{
	Provider: "forge", Name: "review", Delivery: "review",
	Facts: []FactSpec{{Name: reviewEvent, Pointer: "/event"}},
}

// factIn holds when facts[name] is a string among values: an allow-list.
type factIn struct {
	name   string
	values []string
}

func (f factIn) Holds(facts map[string]any, _ time.Time) (bool, error) {
	s, _ := facts[f.name].(string)
	for _, v := range f.values {
		if s == v {
			return true, nil
		}
	}
	return false, nil
}

// factNot holds when facts[name] is not value: a deny-list, which an
// absent fact satisfies.
type factNot struct {
	name  string
	value string
}

func (f factNot) Holds(facts map[string]any, _ time.Time) (bool, error) {
	s, _ := facts[f.name].(string)
	return s != f.value, nil
}

func reviewDraft(payload string) Draft {
	d := Draft{Unit: unit.Unit{Provider: "forge", Key: "acme/api#7"}, Kind: "review", Delivery: "review"}
	if payload != "" {
		d.Payload = json.RawMessage(payload)
	}
	return d
}

func TestRuleReadsPayloadFact(t *testing.T) {
	p := Policy{Default: always, Rules: []Rule{{
		When:     factIn{reviewEvent, []string{"COMMENT", "REQUEST_CHANGES"}},
		Approval: none, Names: "review", Payload: []string{reviewEvent},
	}}}
	for event, want := range map[string]Approval{"COMMENT": none, "REQUEST_CHANGES": none, "APPROVE": always} {
		got, err := p.Decide(reviewDraft(`{"event":"`+event+`","comments":[]}`), Subject{Kind: reviewSpec}, true, time.Time{})
		if err != nil || got.Approval != want {
			t.Errorf("event %s: got %+v, %v; want %+v", event, got, err, want)
		}
	}
	if got := reviewDraft(`{"event":"COMMENT"}`).PayloadFacts(reviewSpec); got[reviewEvent] != "COMMENT" {
		t.Errorf("payload facts: %v", got)
	}
	nested := KindSpec{Provider: "forge", Name: "review", Facts: []FactSpec{{Name: "draft.forge.review.a_b", Pointer: "/a~1b/c~0d/1"}}}
	if got := reviewDraft(`{"a/b":{"c~d":["x","y"]}}`).PayloadFacts(nested); got["draft.forge.review.a_b"] != "y" {
		t.Errorf("escaped pointer: %v", got)
	}
}

func TestAbsentPayloadFactNeverReleases(t *testing.T) {
	deny := Rule{When: factNot{reviewEvent, "APPROVE"}, Approval: none, Names: "review", Payload: []string{reviewEvent}}
	allow := Rule{When: factIn{reviewEvent, []string{"COMMENT"}}, Approval: none, Names: "review", Payload: []string{reviewEvent}}
	held := Rule{When: factIs{reviewEvent, "APPROVE"}, Approval: always, Payload: []string{reviewEvent}}
	byDelivery := Rule{When: factIs{"draft.delivery", "review"}, Approval: none, Names: "review"}
	for name, c := range map[string]struct {
		p Policy
		d Draft
		s Subject
	}{
		"deny-list, no payload":            {Policy{Default: always, Rules: []Rule{deny}}, reviewDraft(""), Subject{Kind: reviewSpec}},
		"deny-list, field missing":         {Policy{Default: always, Rules: []Rule{deny}}, reviewDraft(`{"comments":[]}`), Subject{Kind: reviewSpec}},
		"deny-list, field not a string":    {Policy{Default: always, Rules: []Rule{deny}}, reviewDraft(`{"event":1}`), Subject{Kind: reviewSpec}},
		"deny-list, no kind spec":          {Policy{Default: always, Rules: []Rule{deny}}, reviewDraft(`{"event":"APPROVE"}`), Subject{}},
		"deny-list, payload not an object": {Policy{Default: always, Rules: []Rule{deny}}, reviewDraft(`"APPROVE"`), Subject{Kind: reviewSpec}},
		"deny-list, payload not JSON":      {Policy{Default: always, Rules: []Rule{deny}}, reviewDraft(`{`), Subject{Kind: reviewSpec}},
		"allow-list, no payload":           {Policy{Default: always, Rules: []Rule{allow}}, reviewDraft(""), Subject{Kind: reviewSpec}},
		"a held rule that cannot read the fact lets no later rule release": {
			Policy{Default: always, Rules: []Rule{held, byDelivery}}, reviewDraft(`{"comments":[]}`), Subject{Kind: reviewSpec},
		},
		"a spec of another kind gives no facts": {
			Policy{Default: always, Rules: []Rule{held, byDelivery}}, reviewDraft(`{"event":"APPROVE"}`), Subject{Kind: KindSpec{Provider: "forge", Name: "comment", Facts: reviewSpec.Facts}},
		},
	} {
		got, err := c.p.Decide(c.d, c.s, true, time.Time{})
		if got.Approval.Releases() || got.PostReleased {
			t.Errorf("%s: released: %+v", name, got)
		}
		if err == nil {
			t.Errorf("%s: no error says why the draft waits", name)
		}
	}
}

func TestOtherProviderFactAbsent(t *testing.T) {
	deny := Rule{When: factNot{reviewEvent, "APPROVE"}, Approval: none, Names: "message", Payload: []string{reviewEvent}}
	held := Rule{When: factIs{reviewEvent, "APPROVE"}, Approval: always, Payload: []string{reviewEvent}}
	byDelivery := Rule{When: factIs{"draft.delivery", "message"}, Approval: none, Names: "message"}
	chat := Draft{Unit: unit.Unit{Provider: "chat", Key: "C0123"}, Kind: "reply", Delivery: "message", Payload: json.RawMessage(`{"event":"COMMENT"}`)}
	spec := KindSpec{Provider: "chat", Name: "reply", Delivery: "message"}

	if got := chat.PayloadFacts(spec); len(got) != 0 {
		t.Errorf("a chat reply has payload facts %v", got)
	}
	if got := chat.PayloadFacts(reviewSpec); len(got) != 0 {
		t.Errorf("a chat reply reads forge's review facts: %v", got)
	}
	got, err := (Policy{Default: always, Rules: []Rule{deny}}).Decide(chat, Subject{Kind: spec}, true, time.Time{})
	if err != nil || got.Approval.Releases() || got.Rule != -1 {
		t.Errorf("a deny-list rule over forge's fact released a chat draft: %+v, %v", got, err)
	}
	got, err = (Policy{Default: always, Rules: []Rule{held, byDelivery}}).Decide(chat, Subject{Kind: spec}, true, time.Time{})
	if err != nil || got.Approval != none || got.Rule != 1 {
		t.Errorf("a rule over forge's fact stopped a chat draft its own rule sends: %+v, %v", got, err)
	}
}
