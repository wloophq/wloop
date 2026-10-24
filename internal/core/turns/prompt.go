package turns

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// TypedEcho is how long after the loop typed a line its prompt hook may
// arrive and still be the loop's. A line queued behind a busy agent may
// arrive later: the first prompt after it that matches it is the loop's.
const TypedEcho = 30 * time.Second

// PromptSubmitted notes a prompt the handle's session took, from its prompt
// hook: the user's, unless it is the line the loop typed within TypedEcho. A
// session the registry does not know is left alone.
func (s *Service) PromptSubmitted(ctx context.Context, handle, prompt string) error {
	return s.updateSession(ctx, handle, func(cur *turn.Session, now time.Time) (string, string, string) {
		cur.PromptSeenAt = now
		who := "loop"
		if !loopLine(*cur, prompt, now) {
			cur.UserPromptAt, who = now, "user"
		}
		return KindSessionPrompted, handle + ":" + now.Format(time.RFC3339Nano), who
	})
}

func loopLine(sess turn.Session, prompt string, now time.Time) bool {
	typed := strings.TrimSpace(sess.TypedLine)
	if typed == "" || typed != strings.TrimSpace(prompt) {
		return false
	}
	return now.Sub(sess.TypedAt).Abs() <= TypedEcho || sess.PromptSeenAt.Before(sess.TypedAt)
}

// updateSession applies change to the handle's stored session and saves it
// with the event change names; a handle the registry does not know is left
// alone.
func (s *Service) updateSession(ctx context.Context, handle string, change func(cur *turn.Session, now time.Time) (kind, key, data string)) error {
	s.mu.Lock()
	cur, ok, err := s.Registry.Session(ctx, handle)
	if err != nil || !ok {
		s.mu.Unlock()
		return err
	}
	now := s.now()
	kind, key, data := change(&cur, now)
	ev := s.event(unit.Unit{}, kind, key, data)
	err = s.Registry.SaveSession(ctx, cur, ev)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	s.publish(ev)
	return nil
}

// Typing marks the loop typing into the handle's session until done is
// called; session.close refuses it meanwhile, before the turn names the
// handle and before TypedAt moves.
func (s *Service) Typing(handle string) (done func()) {
	s.mu.Lock()
	if s.typing == nil {
		s.typing = map[string]int{}
	}
	s.typing[handle]++
	s.mu.Unlock()
	return sync.OnceFunc(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.typing[handle]--; s.typing[handle] <= 0 {
			delete(s.typing, handle)
		}
	})
}

func (s *Service) typingInto(handle string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.typing[handle] > 0
}
