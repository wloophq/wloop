package turns_test

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/turn"
)

func TestUserDrivenReadsTheSessionAndItsTurns(t *testing.T) {
	ctx := context.Background()
	e := newCloseEnv(t, cleanupCase{sess: quietSession("term_1")})
	if got, err := e.s.UserDriven(ctx, "term_1"); err != nil || got {
		t.Fatalf("the loop typed last: %v %v", got, err)
	}
	if err := e.s.PromptSubmitted(ctx, "term_1", "what is left?"); err != nil {
		t.Fatal(err)
	}
	if got, err := e.s.UserDriven(ctx, "term_1"); err != nil || !got {
		t.Fatalf("the user typed last: %v %v", got, err)
	}
	done := e.s.Typing("term_1")
	if got, err := e.s.UserDriven(ctx, "term_1"); err != nil || got {
		t.Fatalf("the loop is typing into it: %v %v", got, err)
	}
	done()
	if got, err := e.s.UserDriven(ctx, "term_1"); err != nil || !got {
		t.Fatalf("the loop stopped typing before it noted the line: %v %v", got, err)
	}
	running := doneTurn("term_1", turn.Started)
	e.store.Put(turn.Book{Unit: ref(len("term_1")), Turns: running})
	if got, err := e.s.UserDriven(ctx, "term_1"); err != nil || got {
		t.Fatalf("a loop turn runs in it: %v %v", got, err)
	}
	if got, err := e.s.UserDriven(ctx, "term_9"); err != nil || got {
		t.Fatalf("an unknown session: %v %v", got, err)
	}
}

func TestAnAnswerGivenThroughASessionNamesItInItsEvent(t *testing.T) {
	for _, c := range []struct {
		name, session, actor string
	}{{"the person", "", ""}, {"a session the user drives", "s-mine", "agent"}} {
		t.Run(c.name, func(t *testing.T) {
			s, store, _, _, _ := newService(t)
			putTurn(store, true)
			if _, err := s.Answer(context.Background(), turns.AnswerRequest{Unit: r7, Message: 9, Gate: 4, Text: "go ahead", Source: turn.Cockpit, BySession: c.session}); err != nil {
				t.Fatal(err)
			}
			answered := kinds(store.Events(), turns.KindAnswered)
			if len(answered) != 1 || answered[0].Actor != c.actor || answered[0].Session != c.session {
				t.Fatalf("answered events %+v, want actor %q and session %q", answered, c.actor, c.session)
			}
		})
	}
}
