package turns

// Begun counts the headless turns the service still maps to their units.
func (s *Service) Begun() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.begun)
}

// Routing marks answer id as claimed by a Deliver.
func (s *Service) Routing(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.delivering == nil {
		s.delivering = map[int64]struct{}{}
	}
	s.delivering[id] = struct{}{}
}

// Unlocked runs f when no call holds the service's lock, and says whether it
// ran: whether a concurrent call could get in at this point.
func (s *Service) Unlocked(f func()) bool {
	if !s.mu.TryLock() {
		return false
	}
	s.mu.Unlock()
	f()
	return true
}
