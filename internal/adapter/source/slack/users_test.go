package slack

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	dslack "github.com/wloophq/wloop/internal/domain/slack"
)

func TestUserCacheFetchesOnce(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f.p.deliver(ctx, delta(t, event(fmt.Sprintf("Ev%d", i), evMsg(dm1, "im", other, ts(1+i), "burst"))))
		}()
	}
	wg.Wait()
	if n := len(f.api.Calls("users.info")); n != 1 {
		t.Fatalf("users.info calls %d", n)
	}
	if es := f.p.src.inbox.peek([]string{dm1})[dm1]; len(es) != 100 {
		t.Fatalf("admitted %d", len(es))
	}
	f.now = f.now.Add(UserTTL - time.Minute)
	f.build(t)
	f.login(t)
	f.p.deliver(ctx, delta(t, event("Ev-late", evMsg(dm1, "im", other, ts(200), "later"))))
	if n := len(f.api.Calls("users.info")); n != 1 {
		t.Fatalf("a fresh stored user was fetched again: %d calls", n)
	}
	f.now = f.now.Add(2 * time.Minute)
	f.p.deliver(ctx, delta(t, event("Ev-stale", evMsg(dm1, "im", other, ts(201), "stale"))))
	if n := len(f.api.Calls("users.info")); n != 2 {
		t.Fatalf("a user older than a day was not fetched again: %d calls", n)
	}
}

func TestUserCacheCallersShareOneLoad(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var loads atomic.Int32
		release := make(chan struct{})
		c := &userCache{now: func() time.Time { return t0 }, load: func(ctx context.Context, id string) (dslack.User, error) {
			loads.Add(1)
			<-release
			return dslack.User{ID: id, Name: "person", Fetched: t0}, nil
		}}
		free := sync.OnceFunc(func() { close(release) })
		var wg sync.WaitGroup
		t.Cleanup(func() {
			free()
			wg.Wait()
		})
		got := make(chan dslack.User, 100)
		for range 100 {
			wg.Go(func() {
				u, err := c.get(context.Background(), other)
				if err != nil {
					t.Error(err)
				}
				got <- u
			})
		}
		synctest.Wait()
		if n := loads.Load(); n != 1 {
			t.Fatalf("%d loads while one was in flight", n)
		}
		free()
		for range 100 {
			if u := <-got; u.ID != other || u.Name != "person" {
				t.Fatalf("user %+v", u)
			}
		}
		if n := loads.Load(); n != 1 {
			t.Fatalf("%d loads", n)
		}
	})
}
