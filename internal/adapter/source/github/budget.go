package ghsource

import (
	"github.com/wloophq/wloop/internal/api"
	dgh "github.com/wloophq/wloop/internal/domain/github"
)

// Limiter is a GitHub client's budget.
type Limiter interface {
	Allow(p dgh.Priority) bool
	Budget() map[dgh.Resource]dgh.Limit
}

// Budget is the GitHub budget as the daemon reads it.
type Budget struct{ Limiter Limiter }

// Low says the budget refuses background reads.
func (b Budget) Low() bool { return !b.Limiter.Allow(dgh.PriorityBackground) }

// RateLimit is the GraphQL bucket, which the searches and the sweeps spend.
func (b Budget) RateLimit() api.RateLimit {
	l := b.Limiter.Budget()[dgh.ResourceGraphQL]
	return api.RateLimit{Remaining: l.Remaining, Limit: l.Limit, ResetAt: l.ResetAt}
}
