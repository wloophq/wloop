package outboxtest

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestTheFakeKindsLookUpByProviderAndName(t *testing.T) {
	t.Parallel()
	review := draft.KindSpec{Provider: "acme", Name: "review", Delivery: "review", Basis: draft.BasisAgent}
	k := Kinds{Specs: []draft.KindSpec{review, {Provider: "chat", Name: "review", Delivery: "message"}},
		Fields: map[string][]string{"acme/review": {"event"}}, Required: map[string][]string{"acme/review": {"event"}}}
	if got, ok := k.Lookup("acme", "review"); !ok || got.Delivery != "review" {
		t.Fatalf("Lookup = %+v, %v", got, ok)
	}
	if _, ok := k.Lookup("acme", "nudge"); ok {
		t.Fatal("Lookup found an undeclared kind")
	}
	if err := k.Validate(review, json.RawMessage(`{"event":"COMMENT"}`)); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{``, `{}`, `[1]`, `{"verdict":"x"}`} {
		if err := k.Validate(review, json.RawMessage(bad)); !errors.Is(err, outbox.ErrInvalid) {
			t.Errorf("Validate(%s) = %v, want ErrInvalid", bad, err)
		}
	}
}

func TestTheFakeBasesAnswerTheLastSet(t *testing.T) {
	t.Parallel()
	u := unit.Unit{Provider: "acme", Key: "acme/api#7"}
	b := &Bases{}
	b.Set(u, "abc")
	b.Set(u, "def")
	if got, err := b.Live(context.Background(), u.Provider, u, "review"); err != nil || got != "def" || b.Calls() != 1 {
		t.Fatalf("Live = %q, %v after %d calls", got, err, b.Calls())
	}
}

func TestTheFakeKindsShapeAsTheCatalog(t *testing.T) {
	t.Parallel()
	k := Kinds{Specs: []draft.KindSpec{
		{Provider: "chat", Name: "react", Delivery: "reaction"},
		{Provider: "chat", Name: "reply", Delivery: "message"},
		{Provider: "chat", Name: "nudge", Delivery: "message"},
	}, Defaults: map[string]string{"chat": "message"}}
	for _, c := range []struct {
		name string
		in   draft.Draft
		kind draft.Kind
	}{
		{"no kind and no delivery takes the default delivery, not the first kind", draft.Draft{Purpose: "thanks"}, "reply"},
		{"an undeclared delivery takes the default", draft.Draft{Purpose: "thanks", Delivery: "fax"}, "reply"},
		{"a purpose naming a kind", draft.Draft{Purpose: "nudge"}, "nudge"},
		{"a named kind", draft.Draft{Kind: "react"}, "react"},
	} {
		got, err := k.Shape("chat", c.in)
		if err != nil || got.Kind != c.kind {
			t.Errorf("%s: %+v %v", c.name, got, err)
		}
	}
	if _, err := k.Shape("chat", draft.Draft{Kind: "approve"}); !errors.Is(err, outbox.ErrInvalid) {
		t.Errorf("an undeclared kind: %v", err)
	}
	if got, err := k.Shape("task", draft.Draft{Kind: "reply", Text: "x"}); err != nil || got.Purpose != "reply" {
		t.Errorf("a provider with no kinds: %+v %v", got, err)
	}
}
