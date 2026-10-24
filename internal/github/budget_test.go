package github

import (
	"context"
	"errors"
	"fmt"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func rateLimitBody(remaining, limit int, reset time.Time) string {
	return fmt.Sprintf(`{"data":{"viewer":{"login":"me"},"rateLimit":{"cost":1,"remaining":%d,"limit":%d,"resetAt":%q}}}`,
		remaining, limit, reset.UTC().Format(time.RFC3339))
}

func limitHeader(res dgh.Resource, remaining, limit int, reset time.Time) http.Header {
	return http.Header{
		"X-Ratelimit-Resource":  {string(res)},
		"X-Ratelimit-Limit":     {strconv.Itoa(limit)},
		"X-Ratelimit-Remaining": {strconv.Itoa(remaining)},
		"X-Ratelimit-Reset":     {strconv.FormatInt(reset.Unix(), 10)},
	}
}

func TestBudgetRefusesByPriority(t *testing.T) {
	clk := newClock()
	reset := clk.Now().Add(time.Hour)
	var sent atomic.Int32
	c := fakeClient(transportFunc(func(r *http.Request) (*http.Response, error) {
		sent.Add(1)
		return reply(r, http.StatusOK, nil, rateLimitBody(2000, 5000, reset)), nil
	}), Options{Now: clk.Now, Floors: DefaultFloors(0.5)})
	ctx := context.Background()
	background := dgh.WithPriority(ctx, dgh.PriorityBackground)

	if !c.Allow(dgh.PriorityBackground) {
		t.Fatal("an unknown bucket refused")
	}
	if _, _, err := c.GraphQL(background, viewerQuery, nil); err != nil {
		t.Fatalf("first read: %v", err)
	}
	if c.Allow(dgh.PriorityBackground) || !c.Allow(dgh.PriorityUser) {
		t.Fatalf("at 40 %%: background allowed %v, user allowed %v", c.Allow(dgh.PriorityBackground), c.Allow(dgh.PriorityUser))
	}
	_, _, err := c.GraphQL(background, viewerQuery, nil)
	var be *dgh.BudgetError
	if !errors.As(err, &be) || !errors.Is(err, dgh.ErrBudget) || be.Priority != dgh.PriorityBackground || be.Limit.Remaining != 2000 || sent.Load() != 1 {
		t.Fatalf("background at 40 %%: %v, %d requests", err, sent.Load())
	}
	if _, _, err := c.GraphQL(ctx, viewerQuery, nil); err != nil || sent.Load() != 2 {
		t.Fatalf("user at 40 %%: %v, %d requests", err, sent.Load())
	}

	c.SetFloors(DefaultFloors(0.3))
	if _, _, err := c.GraphQL(background, viewerQuery, nil); err != nil || sent.Load() != 3 {
		t.Fatalf("background above a 0.3 floor: %v, %d requests", err, sent.Load())
	}
}

func TestBudgetUserKeepsTheReserve(t *testing.T) {
	clk := newClock()
	c := fakeClient(transportFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatal("a request was sent")
		return nil, nil
	}), Options{Now: clk.Now})
	c.Restore([]dgh.Limit{{Resource: dgh.ResourceGraphQL, Limit: 5000, Remaining: 99, ResetAt: clk.Now().Add(time.Hour)}})
	if c.Allow(dgh.PriorityUser) {
		t.Fatal("user allowed below 2 %")
	}
	if _, _, err := c.GraphQL(context.Background(), viewerQuery, nil); !errors.Is(err, dgh.ErrBudget) {
		t.Fatalf("user below 2 %%: %v", err)
	}
}

func TestBudgetTwoBuckets(t *testing.T) {
	clk := newClock()
	later, sooner := clk.Now().Add(50*time.Minute), clk.Now().Add(20*time.Minute)
	var sent atomic.Int32
	c := fakeClient(transportFunc(func(r *http.Request) (*http.Response, error) {
		if sent.Add(1)%2 == 1 {
			return reply(r, http.StatusOK, limitHeader(dgh.ResourceGraphQL, 4914, 5000, later), okQuery), nil
		}
		return reply(r, http.StatusOK, limitHeader(dgh.ResourceGraphQL, 4188, 5000, sooner), okQuery), nil
	}), Options{Now: clk.Now})
	for range 4 {
		if _, _, err := c.GraphQL(context.Background(), viewerQuery, nil); err != nil {
			t.Fatal(err)
		}
	}
	if got := c.Budget()[dgh.ResourceGraphQL]; got.Remaining != 4188 || !got.ResetAt.Equal(sooner) {
		t.Fatalf("before the sooner reset: %+v", got)
	}
	clk.Add(20 * time.Minute)
	if got := c.Budget()[dgh.ResourceGraphQL]; got.Remaining != 4914 || !got.ResetAt.Equal(later) {
		t.Fatalf("after the sooner reset: %+v", got)
	}
	clk.Add(30 * time.Minute)
	if got, ok := c.Budget()[dgh.ResourceGraphQL]; ok {
		t.Fatalf("after every reset: %+v", got)
	}
}

func TestUserSlotNotQueued(t *testing.T) {
	inServer := make(chan struct{}, 4)
	release := make(chan struct{})
	var inFlight, most atomic.Int32
	c := fakeClient(transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			return reply(r, http.StatusOK, nil, `{}`), nil
		}
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for m := most.Load(); n > m && !most.CompareAndSwap(m, n); m = most.Load() {
		}
		inServer <- struct{}{}
		<-release
		return reply(r, http.StatusOK, nil, okQuery), nil
	}), Options{})
	background := dgh.WithPriority(context.Background(), dgh.PriorityBackground)
	done := make(chan error, 2)
	for range 2 {
		go func() {
			_, _, err := c.GraphQL(background, viewerQuery, nil)
			done <- err
		}()
	}
	<-inServer

	user := make(chan error, 1)
	go func() {
		_, err := c.REST(context.Background(), Request{Path: "user"})
		user <- err
	}()
	select {
	case err := <-user:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the user read queued behind background reads")
	}
	close(release)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if m := most.Load(); m != 1 {
		t.Fatalf("%d background reads in flight at once", m)
	}
}

func TestQueuedBackgroundReadSeesTheBudgetThatFreedItsSlot(t *testing.T) {
	clk := newClock()
	inServer := make(chan struct{}, 2)
	release := make(chan struct{})
	var sent atomic.Int32
	c := fakeClient(transportFunc(func(r *http.Request) (*http.Response, error) {
		sent.Add(1)
		inServer <- struct{}{}
		<-release
		return reply(r, http.StatusOK, limitHeader(dgh.ResourceGraphQL, 100, 5000, clk.Now().Add(time.Hour)), okQuery), nil
	}), Options{Now: clk.Now})
	background := dgh.WithPriority(context.Background(), dgh.PriorityBackground)
	first := make(chan error, 1)
	go func() {
		_, _, err := c.GraphQL(background, viewerQuery, nil)
		first <- err
	}()
	<-inServer
	second := make(chan error, 1)
	go func() {
		_, _, err := c.GraphQL(background, viewerQuery, nil)
		second <- err
	}()
	time.Sleep(20 * time.Millisecond)
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; !errors.Is(err, dgh.ErrBudget) || sent.Load() != 1 {
		t.Fatalf("the queued read: %v, %d requests", err, sent.Load())
	}
}

func TestRestoreDropsResetBucket(t *testing.T) {
	clk := newClock()
	c := fakeClient(transportFunc(func(r *http.Request) (*http.Response, error) {
		return reply(r, http.StatusOK, nil, okQuery), nil
	}), Options{Now: clk.Now})
	c.Restore([]dgh.Limit{
		{Resource: dgh.ResourceGraphQL, Limit: 5000, Remaining: 10, ResetAt: clk.Now().Add(-time.Minute)},
		{Resource: dgh.ResourceCore, Limit: 5000, Remaining: 4000, ResetAt: clk.Now().Add(time.Hour)},
	})
	b := c.Budget()
	if _, ok := b[dgh.ResourceGraphQL]; ok || b[dgh.ResourceCore].Remaining != 4000 || len(b) != 1 {
		t.Fatalf("restored %+v", b)
	}
	if !c.Allow(dgh.PriorityBackground) {
		t.Fatal("a reset bucket refused")
	}
}
