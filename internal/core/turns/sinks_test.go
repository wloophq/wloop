package turns_test

import (
	"context"
	"errors"
	"testing"

	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// sinks are the three ways a runner records a turn, each given the data
// the runner rendered.
var sinks = map[string]func(context.Context, *turns.Service, turns.Turn, turns.SendRequest) error{
	"send": func(ctx context.Context, s *turns.Service, x turns.Turn, r turns.SendRequest) error {
		_, err := s.Send(ctx, x, r)
		return err
	},
	"start by hand": func(ctx context.Context, s *turns.Service, x turns.Turn, r turns.SendRequest) error {
		_, err := s.StartByHand(ctx, x, r, "")
		return err
	},
	"begin headless": func(ctx context.Context, s *turns.Service, x turns.Turn, r turns.SendRequest) error {
		_, err := s.BeginHeadless(ctx, x, turns.HeadlessStart{Unit: r.Unit, Intent: r.Intent, Turn: r.Turn, Trigger: r.Trigger, Guidance: r.Guidance,
			Inputs: r.Inputs, ResumeOf: r.ResumeOf})
		return err
	},
}

func TestEverySinkRefusesATurnNoStartAdmitted(t *testing.T) {
	u := unit.Unit{Provider: "thread", Key: "1"}
	for name, sink := range sinks {
		t.Run(name, func(t *testing.T) {
			s, store, _, _, _ := newService(t)
			err := sink(context.Background(), s, turns.Turn{}, turns.SendRequest{Unit: u, Intent: "act", Turn: "a", Trigger: turn.ByHand, Records: true})
			if !errors.Is(err, turns.ErrNoTurn) {
				t.Fatalf("a zero turn: %v, want ErrNoTurn", err)
			}
			if b := bookOf(t, store, u); len(b.Turns) != 0 {
				t.Fatalf("a zero turn recorded %+v", b.Turns)
			}
		})
	}
}

func TestEverySinkRecordsATurnOnce(t *testing.T) {
	u := unit.Unit{Provider: "thread", Key: "1"}
	for name, sink := range sinks {
		t.Run(name, func(t *testing.T) {
			s, store, _, _, _ := newService(t)
			x := turnstest.Started(turns.Request{Unit: u, Intent: "act", Trigger: turn.ByHand})
			if err := sink(context.Background(), s, x, turns.SendRequest{Unit: u, Intent: "act", Turn: "a", Trigger: turn.ByHand, Records: true}); err != nil {
				t.Fatal(err)
			}
			err := sink(context.Background(), s, x, turns.SendRequest{Unit: u, Intent: "act", Turn: "b", Trigger: turn.ByHand, Records: true})
			if !errors.Is(err, turns.ErrUsedTurn) {
				t.Fatalf("a used turn: %v, want ErrUsedTurn", err)
			}
			if b := bookOf(t, store, u); len(b.Turns) != 1 || b.Turns[0].ID != "a" {
				t.Fatalf("turns %+v, want only a", b.Turns)
			}
		})
	}
}

func TestEverySinkRecordsTheIdentityItsTurnWasAdmittedWith(t *testing.T) {
	admitted, rendered := unit.Unit{Provider: "thread", Key: "1"}, unit.Unit{Provider: "thread", Key: "2"}
	for name, sink := range sinks {
		t.Run(name, func(t *testing.T) {
			s, store, _, _, _ := newService(t)
			x := turnstest.Started(turns.Request{Unit: admitted, Intent: "act", Guidance: "look at the parser", Inputs: map[string]any{"depth": "shallow"},
				Trigger: turn.Automated, ResumeOf: 3})
			r := turns.SendRequest{Unit: rendered, Intent: "chat", Turn: "a", Trigger: turn.ByHand, Guidance: "rendered guidance", ResumeOf: 9, Records: true}
			if err := sink(context.Background(), s, x, r); err != nil {
				t.Fatal(err)
			}
			if b := bookOf(t, store, rendered); len(b.Turns) != 0 {
				t.Fatalf("the render's unit got %+v", b.Turns)
			}
			b := bookOf(t, store, admitted)
			if len(b.Turns) != 1 {
				t.Fatalf("turns %+v, want one", b.Turns)
			}
			got := b.Turns[0]
			if got.Intent != "act" || got.Trigger != turn.Automated || got.Guidance != "look at the parser" || got.ResumeOf != 3 {
				t.Fatalf("recorded %s %s %q resuming %d, want the admitted act automated with its guidance resuming 3", got.Intent, got.Trigger, got.Guidance, got.ResumeOf)
			}
		})
	}
}

func TestAHeadlessTurnRecordsTheInputsItsTurnWasAdmittedWith(t *testing.T) {
	s, store, _, _, _ := newService(t)
	u := unit.Unit{Provider: "thread", Key: "1"}
	x := turnstest.Started(turns.Request{Unit: u, Intent: "act", Inputs: map[string]any{"depth": "shallow"}})
	if _, err := s.BeginHeadless(context.Background(), x, turns.HeadlessStart{Turn: "a", Inputs: map[string]any{"depth": "deep"}, Items: []string{"i1"}}); err != nil {
		t.Fatal(err)
	}
	got := bookOf(t, store, u).Turns[0]
	if got.Inputs["depth"] != "shallow" || len(turns.ItemsOf(got)) != 1 {
		t.Fatalf("inputs %v, want the admitted depth and the runner's item", got.Inputs)
	}
}
