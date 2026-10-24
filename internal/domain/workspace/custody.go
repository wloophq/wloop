package workspace

import (
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
)

// Custody is a worktree in the custody of one manager, its owner, as the loop
// records it: the store, not the manager, says who owns a worktree and which
// units it stands for.
type Custody struct {
	Path string
	// Repo is the main checkout's path, "" when unknown.
	Repo    string
	Manager string
	// WorkspaceID is the owner's id of the worktree.
	WorkspaceID string
	// CreatedBy is the manager that created the worktree, "" for one the loop
	// found.
	CreatedBy string
	At        time.Time
	Units     []unit.Unit
}
