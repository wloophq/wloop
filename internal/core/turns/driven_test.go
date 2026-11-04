package turns_test

import (
	"context"
	"testing"

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
	running := doneTurn("term_1", turn.Started)
	e.store.Put(turn.Book{Unit: ref(len("term_1")), Turns: running})
	if got, err := e.s.UserDriven(ctx, "term_1"); err != nil || got {
		t.Fatalf("a loop turn runs in it: %v %v", got, err)
	}
	if got, err := e.s.UserDriven(ctx, "term_9"); err != nil || got {
		t.Fatalf("an unknown session: %v %v", got, err)
	}
}
