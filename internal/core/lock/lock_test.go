package lock_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/wloophq/wloop/internal/core/lock"
)

func TestLockWaitsForUnlock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var mu lock.Mutex
		mu.Lock()
		got := make(chan struct{})
		go func() {
			mu.Lock()
			close(got)
		}()
		synctest.Wait()
		select {
		case <-got:
			t.Fatal("Lock returned while the mutex was held")
		default:
		}
		mu.Unlock()
		synctest.Wait()
		select {
		case <-got:
		default:
			t.Fatal("Lock did not return after Unlock")
		}
		mu.Unlock()
	})
}

func TestTryLock(t *testing.T) {
	var mu lock.Mutex
	if !mu.TryLock() {
		t.Fatal("TryLock of a free mutex failed")
	}
	if mu.TryLock() {
		t.Fatal("TryLock of a held mutex succeeded")
	}
	mu.Unlock()
	if !mu.TryLock() {
		t.Fatal("TryLock after Unlock failed")
	}
}

func TestLockContextGivesUpWhenTheContextEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var mu lock.Mutex
		mu.Lock()
		ctx, cancel := context.WithCancel(context.Background())
		errc := make(chan error, 1)
		go func() { errc <- mu.LockContext(ctx) }()
		synctest.Wait()
		cancel()
		if err := <-errc; !errors.Is(err, context.Canceled) {
			t.Fatalf("LockContext = %v, want context.Canceled", err)
		}
		mu.Unlock()
		if err := mu.LockContext(context.Background()); err != nil {
			t.Fatalf("LockContext of a free mutex = %v", err)
		}
	})
}

func TestUnlockOfAFreeMutexPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Unlock of a free mutex did not panic")
		}
	}()
	var mu lock.Mutex
	mu.Unlock()
}
