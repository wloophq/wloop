// Package lock holds a mutual exclusion lock a waiter can give up on.
package lock

import (
	"context"
	"sync"
)

// Mutex is a mutual exclusion lock whose waiters block on a channel, so a
// caller can stop waiting when its context ends and a synctest bubble sees
// a waiter as durably blocked. The zero value is unlocked.
type Mutex struct {
	once sync.Once
	ch   chan struct{}
}

func (m *Mutex) slot() chan struct{} {
	m.once.Do(func() { m.ch = make(chan struct{}, 1) })
	return m.ch
}

// Lock waits until the mutex is free and takes it.
func (m *Mutex) Lock() { m.slot() <- struct{}{} }

// LockContext is Lock, giving up with ctx's error when ctx ends first.
func (m *Mutex) LockContext(ctx context.Context) error {
	select {
	case m.slot() <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TryLock takes the mutex if it is free and reports whether it did.
func (m *Mutex) TryLock() bool {
	select {
	case m.slot() <- struct{}{}:
		return true
	default:
		return false
	}
}

// Unlock frees the mutex; it panics when the mutex is not held.
func (m *Mutex) Unlock() {
	select {
	case <-m.slot():
	default:
		panic("lock: unlock of unlocked Mutex")
	}
}
