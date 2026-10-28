package tasks

import "sync"

// hookedMutex is a sync.Mutex that tells contended, set by tests only, when a Lock has to wait.
type hookedMutex struct {
	sync.Mutex
	contended func()
}

func (m *hookedMutex) Lock() {
	if m.contended == nil {
		m.Mutex.Lock()
		return
	}
	if m.TryLock() {
		return
	}
	m.contended()
	m.Mutex.Lock()
}
