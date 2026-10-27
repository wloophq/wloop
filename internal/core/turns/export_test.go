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
