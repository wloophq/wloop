package api

import (
	"context"
	"slices"
	"sync"
	"time"
)

// Floors are, per priority, the fraction of Limit below which a call of that
// priority is refused until the bucket resets.
type Floors struct{ User, Background float64 }

// DefaultFloors takes plugins.github.rate_limit_fraction as f.
func DefaultFloors(f float64) Floors { return Floors{User: 0.02, Background: f} }

func (f Floors) of(p Priority) float64 {
	if p == PriorityUser {
		return f.User
	}
	return f.Background
}

type bucketKey struct {
	res   Resource
	reset int64
}

// budget keeps one bucket per (Resource, ResetAt), each at the lowest
// Remaining reported for it.
type budget struct {
	mu      sync.Mutex
	floors  Floors
	buckets map[bucketKey]Limit
}

func (b *budget) observe(l Limit, now time.Time) {
	if l.Resource == "" || l.Limit <= 0 || !now.Before(l.ResetAt) {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	k := bucketKey{l.Resource, l.ResetAt.Unix()}
	if old, ok := b.buckets[k]; ok && old.Remaining <= l.Remaining {
		return
	}
	b.buckets[k] = l
}

// lowest is the live bucket of res with the least Remaining; it drops
// buckets past their reset.
func (b *budget) lowest(res Resource, now time.Time) (Limit, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var low Limit
	found := false
	for k, l := range b.buckets {
		if !now.Before(l.ResetAt) {
			delete(b.buckets, k)
			continue
		}
		if k.res == res && (!found || l.Remaining < low.Remaining) {
			low, found = l, true
		}
	}
	return low, found
}

func (b *budget) refusal(res Resource, p Priority, now time.Time) *BudgetError {
	l, ok := b.lowest(res, now)
	if !ok {
		return nil
	}
	b.mu.Lock()
	floor := b.floors.of(p)
	b.mu.Unlock()
	if float64(l.Remaining) < floor*float64(l.Limit) {
		return &BudgetError{Limit: l, Priority: p}
	}
	return nil
}

// SetFloors replaces the floors the client admits calls by.
func (c *Client) SetFloors(f Floors) {
	c.budget.mu.Lock()
	defer c.budget.mu.Unlock()
	c.budget.floors = f
}

// Allow says, without I/O, whether a call of p would be admitted now on the
// GraphQL bucket, the one every job spends.
func (c *Client) Allow(p Priority) bool {
	return c.budget.refusal(ResourceGraphQL, p, c.now()) == nil
}

// Budget is the lowest live bucket of each resource seen so far.
func (c *Client) Budget() map[Resource]Limit {
	now := c.now()
	out := map[Resource]Limit{}
	for _, res := range []Resource{ResourceGraphQL, ResourceCore, ResourceSearch} {
		if l, ok := c.budget.lowest(res, now); ok {
			out[res] = l
		}
	}
	return out
}

// Restore seeds the budget, as a store kept it; a bucket already reset is
// dropped.
func (c *Client) Restore(limits []Limit) {
	now := c.now()
	for _, l := range limits {
		c.budget.observe(l, now)
	}
}

// slots bounds the requests in flight: every call takes one of all, and a
// call below PriorityUser first takes one of background, which holds one
// fewer, so a user call never queues behind background work.
type slots struct{ all, background chan struct{} }

func newSlots(n int) slots {
	if n < 2 {
		n = 2
	}
	return slots{all: make(chan struct{}, n), background: make(chan struct{}, n-1)}
}

func (s slots) acquire(ctx context.Context, p Priority) (release func(), err error) {
	held := make([]chan struct{}, 0, 2)
	if p != PriorityUser {
		held = append(held, s.background)
	}
	held = append(held, s.all)
	for i, ch := range held {
		select {
		case ch <- struct{}{}:
		case <-ctx.Done():
			for _, taken := range slices.Backward(held[:i]) {
				<-taken
			}
			return nil, ctx.Err()
		}
	}
	return func() {
		for _, ch := range slices.Backward(held) {
			<-ch
		}
	}, nil
}
