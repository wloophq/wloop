package gtd

import "github.com/wloophq/wloop/internal/domain/gtd"

// Handles says whether a handler is registered for verb on entries of kind.
func (s *Service) Handles(kind gtd.Kind, verb gtd.Verb) bool { return s.handler(kind, verb) != nil }

// SetMe replaces the user's identity the next Sync reconciles with.
func (s *Service) SetMe(me gtd.Me) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.d.Me = me
}

// Accepts says whether e's state takes verb with no handler: ErrArgs for an unknown verb, ErrVerb naming the
// verb and the state for a refused one. approve, delegate, answer, edit and retry move nothing on their own,
// so it refuses them.
func Accepts(e gtd.Entry, verb gtd.Verb) error { return accepts(e, verb) }
