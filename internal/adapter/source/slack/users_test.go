package slack

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"testing"
	"time"
)

func TestUserCacheFetchesOnce(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	release := make(chan struct{})
	f.api.Handle("users.info", func(v url.Values) any {
		<-release
		return map[string]any{"user": map[string]any{"id": v.Get("user"), "name": "person"}}
	})
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f.p.deliver(ctx, delta(t, event(fmt.Sprintf("Ev%d", i), evMsg(dm1, "im", other, ts(1+i), "burst"))))
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
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
