// Package placement says where a unit's work happens: which repository,
// checkout and role.
package placement

import (
	"context"

	"github.com/wloophq/wloop/internal/domain/unit"
)

type Placement struct {
	Repo     string // workspace manager repo, "owner/name"; "" none
	Checkout string // "" new worktree, "main" the repo's main checkout, or an absolute path
	Role     string // "review", "author", "work"
	Shared   bool   // the workspace may hold sessions of other units
	Source   string // "override", "context", "suggested", "provider"
}

type Rule struct {
	Match                string // glob over "<provider>:<key>"; "*" matches any run of characters including "/", "?" one character; no other metacharacters
	Repo, Checkout, Role string
	Shared               bool
}

// Map is the context map; first match wins.
type Map struct{ Rules []Rule }

type Overrides interface {
	Override(ctx context.Context, u unit.Unit) (Placement, bool, error)
	SetOverride(ctx context.Context, u unit.Unit, p Placement) error // zero Placement clears
}

// Hints is the extraction's suggestion; nil none.
type Hints interface {
	Suggested(ctx context.Context, u unit.Unit, item string) (Placement, bool, error)
}

// Defaults is per provider; github: owner/repo of the PR, role from the intent.
type Defaults interface {
	Default(ctx context.Context, u unit.Unit, item string) (Placement, bool, error)
}
