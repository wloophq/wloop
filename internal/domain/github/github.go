// Package github holds the values the GitHub client reads and the budget
// its callers spend: pull request states, explore candidates, rate limits
// and the priority a call is tagged with.
package github

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wloophq/wloop/internal/domain/pr"
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
	PriorityBackground                 // jobs, briefs, explore candidates
)

type priorityKey struct{}

// WithPriority tags ctx; an untagged call is PriorityUser.
func WithPriority(ctx context.Context, p Priority) context.Context {
	return context.WithValue(ctx, priorityKey{}, p)
}

// PriorityOf is the priority ctx is tagged with.
func PriorityOf(ctx context.Context) Priority {
	p, _ := ctx.Value(priorityKey{}).(Priority)
	return p
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

// Candidate is a pull request Explore found, with what deciding whether to
// help on it needs.
type Candidate struct {
	Ref            pr.Ref    `json:"ref"`
	URL            string    `json:"url"`
	Title          string    `json:"title"`
	Author         string    `json:"author"`
	State          string    `json:"state"`
	IsDraft        bool      `json:"draft"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	Additions      int       `json:"additions"`
	Deletions      int       `json:"deletions"`
	ChangedFiles   int       `json:"changed_files"`
	HeadBranch     string    `json:"head"`
	BaseBranch     string    `json:"base"`
	ReviewDecision string    `json:"review_decision"`
	ReviewRequests []string  `json:"review_requests"` // logins and team:org/slug
	ChecksState    string    `json:"checks"`
	Comments       int       `json:"comments"`
	// MyReview is the viewer's latest review state, "" when none.
	MyReview       string `json:"my_review"`
	MyReviewOnHead bool   `json:"my_review_on_head"`
}

type CandidatePage struct {
	Candidates []Candidate
	Total      int
	RateLimit  RateLimit
}
