package turns

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wloophq/wloop/internal/domain/usage"
)

// Tier is a provider-neutral class of model; each agent maps it to one
// of its own models.
type Tier string

const (
	// TierFast is the cheapest model, for short mechanical calls.
	TierFast Tier = "fast"
	// TierBalanced is the everyday workhorse.
	TierBalanced Tier = "balanced"
	// TierFrontier is the most capable model.
	TierFrontier Tier = "frontier"
)

var Tiers = []Tier{TierFast, TierBalanced, TierFrontier}

func ParseTier(s string) (Tier, error) {
	for _, t := range Tiers {
		if string(t) == s {
			return t, nil
		}
	}
	return "", fmt.Errorf("unknown model tier %q; use %s, %s or %s", s, TierFast, TierBalanced, TierFrontier)
}

// Ask is one headless call: Prompt in, JSON fitting Schema out, with no MCP
// servers, no user settings and no saved session. Without Tools and Dirs it
// has no tools at all; with either it runs restricted, reading only Dirs.
type Ask struct {
	// Home is the agent's configuration directory, "" for its default.
	Home   string
	Tier   Tier
	Prompt string
	Schema string
	// MaxCostUSD caps the call; 0 leaves it to the agent.
	MaxCostUSD float64
	// Tools are read-only tools the call may use, a subset of ReadOnlyTools; nil is none.
	Tools []string
	// Dirs are absolute directories the call may read (the placed checkout, the dossier).
	Dirs []string
}

var ReadOnlyTools = []string{"Read", "Grep", "Glob"}

type Answer struct {
	JSON []byte
	// Model names the agent's model that answered: the id it reports, else
	// the name it was asked for.
	Model string
	// CostUSD is nil when the agent did not report it.
	CostUSD  *float64
	Duration time.Duration
	// Tokens is nil when the agent did not report them.
	Tokens *usage.Tokens
}

// Asker runs the headless calls of one coding agent.
type Asker interface {
	Ask(ctx context.Context, a Ask) (Answer, error)
}

// AskingAgent is an Agent that also answers headless calls: Asker returns
// its caller running in dir, with models pinning tiers to model names of the
// agent's own.
type AskingAgent interface {
	Asker(dir string, models map[Tier]string) (Asker, error)
}

var (
	// ErrToolNotAllowed is a Tools entry outside ReadOnlyTools; nothing is spawned.
	ErrToolNotAllowed = errors.New("the call names a tool that is not read-only")
	// ErrNoRestricted is a call with Tools or Dirs while the installed agent lacks a restricted mode; nothing is spawned.
	ErrNoRestricted = errors.New("the installed agent cannot run a restricted call")
	ErrNoAgent      = errors.New("the agent is not on the PATH")
	// ErrOverBudget is a call that ran past Ask.MaxCostUSD.
	ErrOverBudget = errors.New("the call ran over its budget")
	// ErrUnfit is an answer that is not JSON fitting the schema.
	ErrUnfit = errors.New("the answer does not fit the schema")
	// ErrTooLong is a prompt longer than the model reads.
	ErrTooLong = errors.New("the prompt is too long")
	// ErrUnavailable is an agent that cannot answer now: rate limited,
	// overloaded, out of credit, logged out.
	ErrUnavailable = errors.New("the agent is unavailable")
)
