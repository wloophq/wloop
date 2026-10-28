package tasks

import "github.com/wloophq/wloop/internal/domain/task"

// Handles says whether a handler is registered for verb on entries of kind.
func (s *Service) Handles(kind task.Kind, verb task.Verb) bool { return s.handler(kind, verb) != nil }

// SetMe replaces the user's identity the next Sync reconciles with.
func (s *Service) SetMe(me task.Me) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.d.Me = me
}

// Accepts says whether e's state takes verb with no handler: ErrArgs for an unknown verb, ErrVerb naming the
// verb and the state for a refused one. approve, delegate, answer, edit, retry, reply, reviewed and sequential
// move nothing on their own, so it refuses them.
func (s *Service) Accepts(e task.Task, verb task.Verb) error { return s.accepts(e, verb) }
