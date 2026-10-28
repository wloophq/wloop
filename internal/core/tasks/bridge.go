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
