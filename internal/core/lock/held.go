package lock

import "context"

type heldKey struct{}

// WithHeld marks ctx as run by a caller that holds the loop mutex.
func WithHeld(ctx context.Context) context.Context {
	return context.WithValue(ctx, heldKey{}, true)
}

// Held tells a context whose caller holds the loop mutex: what runs under
// it must not take the mutex again.
func Held(ctx context.Context) bool {
	held, _ := ctx.Value(heldKey{}).(bool)
	return held
}
