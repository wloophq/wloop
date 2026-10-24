package turns_test

import (
	"context"
	"errors"
	"testing"

	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestEndHeadlessForgetsTheTurnsItBegan(t *testing.T) {
	s, _, _, _, _ := newService(t)
	ctx := context.Background()
	u := unit.Unit{Provider: "thread", Key: "1"}
	ended, err := s.BeginHeadless(ctx, turns.HeadlessStart{Unit: u, Intent: "act", Turn: "a", Trigger: turn.ByHand})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.EndHeadless(ctx, ended, turns.HeadlessEnd{Err: errors.New("boom")}); err != nil {
		t.Fatal(err)
	}
	dismissed, err := s.BeginHeadless(ctx, turns.HeadlessStart{Unit: u, Intent: "act", Turn: "b", Trigger: turn.ByHand})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DismissTurn(ctx, u, dismissed, "a rule"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EndHeadless(ctx, dismissed, turns.HeadlessEnd{Err: errors.New("late")}); err == nil {
		t.Fatal("ending a dismissed turn")
	}
	if n := s.Begun(); n != 0 {
		t.Fatalf("%d begun turns kept after their end", n)
	}
}
