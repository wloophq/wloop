package tasks

// SetRaceHooks makes Do call beforeApply between reading a task and writing it, and the service's lock call
// contended when a Lock has to wait. Set them before the service runs.
func SetRaceHooks(s *Service, beforeApply, contended func()) {
	s.beforeApply, s.mu.contended = beforeApply, contended
}
