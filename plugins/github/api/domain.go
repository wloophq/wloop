package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"
)

const (
	StateOpen   = "OPEN"
	StateMerged = "MERGED"
	StateClosed = "CLOSED"
)

// Priority orders who may spend the budget when it runs low.
type Priority uint8

const (
	PriorityUser       Priority = iota // a user action; keeps the reserve
	PriorityBackground                 // jobs and reads the plugin runs on its own
)

type priorityKey struct{}

// WithPriority tags ctx.
func WithPriority(ctx context.Context, p Priority) context.Context {
	return context.WithValue(ctx, priorityKey{}, p)
}

// PriorityOf is the priority ctx is tagged with, else the one the host's
// call carries.
func PriorityOf(ctx context.Context) Priority {
	if p, ok := ctx.Value(priorityKey{}).(Priority); ok {
		return p
	}
	if src.CallOf(ctx).Priority == src.PriorityBackground {
		return PriorityBackground
	}
	return PriorityUser
}

// Resource is a rate-limit bucket family, as x-ratelimit-resource names it.
type Resource string

const (
	ResourceGraphQL Resource = "graphql"
	ResourceCore    Resource = "core"
	ResourceSearch  Resource = "search"
)

// Limit is one bucket as a response reported it.
type Limit struct {
	Resource         Resource
	Limit, Remaining int
	ResetAt          time.Time
}

var ErrBudget = errors.New("GitHub budget low")

// BudgetError is a call refused because its bucket fell below the floor of
// its priority.
type BudgetError struct {
	Limit    Limit
	Priority Priority
}

func (e *BudgetError) Error() string {
	return fmt.Sprintf("GitHub budget low: %d of %d %s left until %s", e.Limit.Remaining, e.Limit.Limit, e.Limit.Resource, e.Limit.ResetAt.Local().Format("15:04"))
}

func (e *BudgetError) Is(target error) bool { return target == ErrBudget }

type RateLimit struct {
	Cost      int       `json:"cost"`
	Remaining int       `json:"remaining"`
	Limit     int       `json:"limit"`
	ResetAt   time.Time `json:"reset_at"`
}
