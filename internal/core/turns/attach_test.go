package turns_test

import (
	"context"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestAnAttachedSessionStopsWithoutBeingAskedForARecord(t *testing.T) {
	s, store, reg, _, _ := newService(t)
	s.Intents = dintent.NewRegistry(append(turnstest.Intents().All(),
		dintent.Def{Name: "pair", Provider: "task", Runners: []string{"session"}, Record: dintent.RecordRules{None: true}}))
	ctx := context.Background()
	for i, c := range []struct {
		intent turn.Intent
		task   unit.Unit
		handle string
	}{
		{"attach", unit.Unit{Provider: unit.Task, Key: "t-00000000000000a1"}, "term_1"},
		{"pair", unit.Unit{Provider: unit.Task, Key: "t-00000000000000b2"}, "term_2"},
	} {
		reg.Put(turn.Session{ID: c.handle, Handle: c.handle, AgentSession: "s" + c.handle, StartedAt: t0, MCPAt: t0})
		id := "turn-" + string(c.intent)
		if _, err := s.StartByHand(ctx, turns.SendRequest{Unit: c.task, Intent: c.intent, Turn: id, Trigger: turn.ByHand, Records: true}, c.handle); err != nil {
			t.Fatal(err)
		}
		for n, prompt := range []string{"p1", "p1", "p2"} {
			got, err := s.Stop(ctx, turns.StopRequest{Session: c.handle, AgentSession: "s" + c.handle, PromptID: prompt, LastMessage: "done here"})
			if err != nil {
				t.Fatal(err)
			}
			if got.Block {
				t.Fatalf("%d %s: stop %d blocked: %s", i, c.intent, n, got.Reason)
			}
		}
		b := bookOf(t, store, c.task)
		if len(b.Turns) != 1 || b.Turns[0].State != turn.Started || len(b.Records) != 0 {
			t.Fatalf("%s: turns %+v records %+v", c.intent, b.Turns, b.Records)
		}
	}
}

func TestEachSessionAttachedToATaskIsARunOfItsOwn(t *testing.T) {
	s, _, reg, _, _ := newService(t)
	ctx := context.Background()
	tk := unit.Unit{Provider: unit.Task, Key: "t-00000000000000a1"}
	for _, h := range []string{"term_1", "term_2"} {
		reg.Put(turn.Session{ID: h, Handle: h, AgentSession: "s" + h, StartedAt: t0, MCPAt: t0})
	}
	attach := func(id, handle string) {
		t.Helper()
		if _, err := s.StartByHand(ctx, turns.SendRequest{Unit: tk, Intent: "attach", Turn: id, Trigger: turn.ByHand, Records: true, PerSession: true}, handle); err != nil {
			t.Fatal(err)
		}
	}
	attach("a1", "term_1")
	attach("a2", "term_2")
	attach("a3", "term_1")
	runs, err := s.RunsOf(ctx, tk)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range runs {
		got = append(got, r.Turn.ID+" "+string(r.Turn.State)+" "+r.Session)
	}
	if want := []string{"a3 started term_1", "a2 started term_2"}; !slices.Equal(got, want) {
		t.Fatalf("runs %v, want %v", got, want)
	}
}
