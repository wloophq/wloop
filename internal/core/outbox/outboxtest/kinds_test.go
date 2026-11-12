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
	if got, err := b.Live(context.Background(), u, "review"); err != nil || got != "def" || b.Calls() != 1 {
		t.Fatalf("Live = %q, %v after %d calls", got, err, b.Calls())
	}
}
