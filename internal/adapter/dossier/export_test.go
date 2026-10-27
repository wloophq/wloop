package dossier

// WaitingForIndexLock is f with wait in place of the pause before a commit retries behind an index lock.
func (f Files) WaitingForIndexLock(wait func()) Files {
	f.waitIndexLock = wait
	return f
}
