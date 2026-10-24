package turnstest_test

import (
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	"github.com/wloophq/wloop/internal/domain/turn"
)

func TestFakeSessions(t *testing.T) {
	turnstest.RunSessions(t, func(*testing.T) (turns.Sessions, func() []turnstest.Typed) {
		s := turnstest.NewWorldSessions()
		return s, s.Typed
	})
}

func TestFakePrompts(t *testing.T) {
	turnstest.RunPrompts(t, func(*testing.T) (turns.Prompts, turnstest.PromptsWorld) {
		at := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
		p := turnstest.NewPrompts()
		p.Put("term_1", at)
		p.Put("term_2", time.Time{})
		return p, turnstest.PromptsWorld{Prompted: turn.Session{ID: "term_1"}, Quiet: turn.Session{ID: "term_2"}, Unknown: turn.Session{ID: "term_3"}, At: at}
	})
}
