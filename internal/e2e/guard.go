// Package e2e holds the loop's live tests, behind the live build tag, and the
// guards every one of their writes goes through: they touch only what the
// tests create in Orca and only the sandbox repository on GitHub.
package e2e

import (
	"fmt"
	"strings"

	"github.com/wloophq/wloop/internal/brand"
)

var (
	// Prefix starts the name of every Orca workspace, branch, terminal and
	// pull request title the live tests create.
	Prefix = brand.Name + "-e2e-"
	// Sandbox is the only repository the live tests write to.
	Sandbox = brand.Org + "/" + brand.Name + "-sandbox"
)

// MustOwn refuses an Orca workspace or branch name the live tests did not
// make.
func MustOwn(name string) error {
	if !strings.HasPrefix(name, Prefix) {
		return fmt.Errorf("refusing %q: the live tests touch only names that start with %s", name, Prefix)
	}
	return nil
}

// MustOwnBranch refuses a branch the live tests did not make. Orca may put
// one segment before the name it was given (`owner/<Prefix>...`).
func MustOwnBranch(branch string) error {
	name := branch
	if owner, rest, ok := strings.Cut(branch, "/"); ok && owner != "" && !strings.Contains(rest, "/") {
		name = rest
	}
	if !strings.HasPrefix(name, Prefix) {
		return fmt.Errorf("refusing branch %q: the live tests touch only branches named %s..., with at most one segment before", branch, Prefix)
	}
	return nil
}

// Workspace is an Orca workspace as the guard reads it: its name, and the
// repository and number of the pull request it is linked to (0 for none).
type Workspace struct {
	Name, Repo string
	PR         int
}

// MustOwnWorkspace accepts a workspace the live tests created, or one the loop
// created for a sandbox pull request, and refuses every other.
func MustOwnWorkspace(ws Workspace) error {
	if MustOwn(ws.Name) == nil {
		return nil
	}
	if ws.PR != 0 && strings.EqualFold(ws.Repo, Sandbox) {
		return nil
	}
	return fmt.Errorf("refusing workspace %q (linked to %s#%d): it is neither the live tests' nor a sandbox pull request's", ws.Name, ws.Repo, ws.PR)
}

// SandboxOnly refuses any repository but the sandbox.
func SandboxOnly(repo string) error {
	if !strings.EqualFold(strings.TrimSpace(repo), Sandbox) {
		return fmt.Errorf("refusing to write to %q: the live tests write only to %s", repo, Sandbox)
	}
	return nil
}
