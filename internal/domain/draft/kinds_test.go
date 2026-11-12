package draft

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/domain/unit"
)

const anyObject = `{"type": "object", "properties": {}}`

// chat is a catalog of one provider, acme, that posts messages and reactions.
func chat(t *testing.T) *Catalog {
	t.Helper()
	c, err := NewCatalog([]KindSpec{
		{Provider: "acme", Name: "reply", Delivery: "message", TextRequired: true, SchemaJSON: json.RawMessage(anyObject)},
		{Provider: "acme", Name: "nudge", Delivery: "message", TextRequired: true, SchemaJSON: json.RawMessage(anyObject)},
		{Provider: "acme", Name: "react", Delivery: "reaction", SchemaJSON: json.RawMessage(anyObject)},
		{Provider: "acme", Name: "verdict", Delivery: "review", SchemaJSON: json.RawMessage(`{"type": "object", "required": ["event"], "additionalProperties": false,
			"properties": {"event": {"type": "string", "enum": ["APPROVE", "COMMENT"]}}}`)},
	}, []DeliverySpec{{Provider: "acme", Name: "message", Posts: true}, {Provider: "acme", Name: "reaction"}, {Provider: "acme", Name: "review", Posts: true}},
		map[string]string{"acme": "message"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestTextRequiredFromSpec(t *testing.T) {
	c := chat(t)
	reply, _ := c.Lookup("acme", "reply")
	if err := reply.CheckText("  \n"); !errors.Is(err, ErrEmptyText) {
		t.Errorf("a blank reply: %v", err)
	}
	if err := reply.CheckText("Thanks"); err != nil {
		t.Errorf("a reply with text: %v", err)
	}
	for _, name := range []string{"react", "verdict"} {
		k, _ := c.Lookup("acme", name)
		if err := k.CheckText(""); err != nil {
			t.Errorf("%s, which requires no text: %v", name, err)
		}
	}
}

func TestPostsFromManifests(t *testing.T) {
	c := chat(t)
	for _, x := range []struct {
		delivery string
		want     bool
	}{{"message", true}, {"review", true}, {"reaction", false}, {"undeclared", true}} {
		if got := c.Posts("acme", x.delivery); got != x.want {
			t.Errorf("Posts(%s) = %v", x.delivery, got)
		}
	}
	if !c.Posts("other", "reaction") {
		t.Error("a delivery of a provider with no manifest does not post")
	}
	for name, want := range map[string]bool{"reply": true, "react": false, "verdict": true} {
		if k, _ := c.Lookup("acme", name); k.Posts != want {
			t.Errorf("%s posts %v", name, k.Posts)
		}
	}
}

func TestShapeFromTheCatalog(t *testing.T) {
	c := chat(t)
	for _, x := range []struct {
		name string
		in   Draft
		want [3]string // kind, purpose, delivery
	}{
		{"a kind", Draft{Kind: "react"}, [3]string{"react", "react", "reaction"}},
		{"a kind with its own purpose", Draft{Kind: "nudge", Purpose: "remind"}, [3]string{"nudge", "remind", "message"}},
		{"a kind's delivery wins", Draft{Kind: "react", Delivery: "message"}, [3]string{"react", "react", "reaction"}},
		{"a purpose that is a kind", Draft{Purpose: "nudge", Delivery: "message"}, [3]string{"nudge", "nudge", "message"}},
		{"a purpose that is a kind of another delivery", Draft{Purpose: "nudge", Delivery: "reaction"}, [3]string{"react", "nudge", "reaction"}},
		{"a label on a message", Draft{Purpose: "thanks", Delivery: "message"}, [3]string{"reply", "thanks", "message"}},
		{"a label and no delivery", Draft{Purpose: "thanks"}, [3]string{"reply", "thanks", "message"}},
		{"a delivery acme does not declare", Draft{Purpose: "thanks", Delivery: "comment"}, [3]string{"reply", "thanks", "message"}},
		{"a purpose that is a kind, on a delivery acme does not declare", Draft{Purpose: "verdict", Delivery: "comment"}, [3]string{"verdict", "verdict", "review"}},
	} {
		t.Run(x.name, func(t *testing.T) {
			got, err := c.Shape("acme", x.in)
			if err != nil {
				t.Fatal(err)
			}
			if g := [3]string{string(got.Kind), got.Purpose, string(got.Delivery)}; g != x.want {
				t.Errorf("shaped %v, want %v", g, x.want)
			}
		})
	}
	if _, err := c.Shape("acme", Draft{Kind: "comment"}); !errors.Is(err, ErrUnknownKind) {
		t.Errorf("an undeclared kind: %v", err)
	}
	got, err := c.Shape("other", Draft{Kind: "comment", Delivery: "x"})
	if err != nil || got.Kind != "comment" || got.Purpose != "comment" || got.Delivery != "x" {
		t.Errorf("a provider with no kinds: %+v, %v", got, err)
	}
}

func TestValidateAgainstTheKindSchema(t *testing.T) {
	c := chat(t)
	verdict, _ := c.Lookup("acme", "verdict")
	if err := c.Validate(verdict, json.RawMessage(`{"event": "COMMENT"}`)); err != nil {
		t.Error(err)
	}
	for _, p := range []string{``, `{}`, `{"event": "MERGE"}`, `{"event": "COMMENT", "x": 1}`, `[]`} {
		if err := c.Validate(verdict, json.RawMessage(p)); !errors.Is(err, ErrPayload) {
			t.Errorf("payload %q: %v", p, err)
		}
	}
	if err := c.Validate(KindSpec{Provider: "acme", Name: "nope"}, nil); !errors.Is(err, ErrUnknownKind) {
		t.Errorf("a kind not in the catalog: %v", err)
	}
}

func TestNewCatalogRefuses(t *testing.T) {
	ok := json.RawMessage(anyObject)
	ds := []DeliverySpec{{Provider: "acme", Name: "message"}}
	for name, kinds := range map[string][]KindSpec{
		"a kind twice":           {{Provider: "acme", Name: "reply", Delivery: "message", SchemaJSON: ok}, {Provider: "acme", Name: "reply", Delivery: "message", SchemaJSON: ok}},
		"an unknown delivery":    {{Provider: "acme", Name: "reply", Delivery: "comment", SchemaJSON: ok}},
		"a schema out of subset": {{Provider: "acme", Name: "reply", Delivery: "message", SchemaJSON: json.RawMessage(`{"type": "object", "oneOf": []}`)}},
	} {
		if _, err := NewCatalog(kinds, ds, nil); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// TestLookupByPublisherProvider checks a draft is shaped by the kinds of the provider that publishes it, which
// the caller names, never by its unit's: a draft on a task unit that a chat plugin posts takes the chat's kinds.
func TestLookupByPublisherProvider(t *testing.T) {
	c, err := NewCatalog([]KindSpec{
		{Provider: "acme", Name: "react", Delivery: "reaction", SchemaJSON: json.RawMessage(anyObject)},
		{Provider: "pulls", Name: "react", Delivery: "emoji", SchemaJSON: json.RawMessage(anyObject)},
		{Provider: "pulls", Name: "comment", Delivery: "comment", TextRequired: true, SchemaJSON: json.RawMessage(anyObject)},
	}, []DeliverySpec{{Provider: "acme", Name: "reaction"}, {Provider: "pulls", Name: "emoji"}, {Provider: "pulls", Name: "comment", Posts: true}},
		map[string]string{"pulls": "comment"})
	if err != nil {
		t.Fatal(err)
	}
	onPull := Draft{Unit: unit.Unit{Provider: "pulls", Key: "o/r#1"}, Kind: "react", Target: "C1/1.2"}
	got, err := c.Shape("acme", onPull)
	if err != nil || got.Delivery != "reaction" {
		t.Errorf("a pull request draft acme posts: %+v, %v", got, err)
	}
	onTask := Draft{Unit: unit.Unit{Provider: "task", Key: "t-1"}, Kind: "comment"}
	if _, err := c.Shape("acme", onTask); !errors.Is(err, ErrUnknownKind) {
		t.Errorf("a kind only the unit's provider declares: %v", err)
	}
	if got, err := c.Shape("pulls", onTask); err != nil || got.Delivery != "comment" {
		t.Errorf("a task draft pulls posts: %+v, %v", got, err)
	}
}

func TestAKindsVerdictIsThePayloadFieldItPointsTo(t *testing.T) {
	review := KindSpec{Verdict: "/event"}
	for payload, want := range map[string]string{`{"event":"APPROVE"}`: "APPROVE", `{"body":"x"}`: "", `[1]`: "", ``: ""} {
		if got := review.VerdictOf(Draft{Payload: json.RawMessage(payload)}); got != want {
			t.Errorf("%s: %q, want %q", payload, got, want)
		}
	}
	if got := (KindSpec{}).VerdictOf(Draft{Payload: json.RawMessage(`{"event":"APPROVE"}`)}); got != "" {
		t.Errorf("a kind that posts no verdict: %q", got)
	}
}

func TestAKindsVerdictValuesAreItsSchemasEnum(t *testing.T) {
	k := KindSpec{Verdict: "/event", SchemaJSON: json.RawMessage(`{"type":"object","properties":{"event":{"type":"string","enum":["LGTM","NEEDS_WORK"]}}}`)}
	if got := k.VerdictValues(); !slices.Equal(got, []string{"LGTM", "NEEDS_WORK"}) {
		t.Fatalf("verdicts %v", got)
	}
	k.Verdict = ""
	if got := k.VerdictValues(); got != nil {
		t.Fatalf("a kind that posts no verdict: %v", got)
	}
}
