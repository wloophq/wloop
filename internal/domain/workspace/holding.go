package workspace

import (
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
)

// Held is what each workspace of one snapshot holds.
type Held interface {
	// Of is the unit of work w holds; false for none.
	Of(w Workspace) (Holding, bool)
}

// Holding is the unit of work a workspace holds, as its source reports it.
type Holding struct {
	Unit unit.Unit
	// Repo is the unit's repository, "owner/name".
	Repo string
	// Review says the workspace reviews someone else's work; otherwise it is
	// where the user develops it.
	Review bool
	// State is the unit's state in its source, and Ended when it ended;
	// zero while open.
	State string
	Ended time.Time
	// Duplicate says another workspace is the one its manager links the
	// unit to.
	Duplicate bool
}
