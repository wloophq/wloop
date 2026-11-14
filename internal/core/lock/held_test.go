package lock_test

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/core/lock"
)

func TestAContextIsHeldOnlyOnceMarked(t *testing.T) {
	ctx := context.Background()
	if lock.Held(ctx) {
		t.Fatal("an unmarked context is held")
	}
	if !lock.Held(lock.WithHeld(ctx)) {
		t.Fatal("a marked context is not held")
	}
}
