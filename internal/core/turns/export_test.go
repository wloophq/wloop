package turns

// Begun counts the headless turns the service still maps to their units.
func (s *Service) Begun() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.begun)
}
