package outbox

import (
	"context"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
)

// Lister lists the Outbox's drafts.
type Lister interface {
	List(ctx context.Context, f Filter) ([]draft.Draft, error)
}

// Undelivered are the approved draft gates of the drafts drafts lists whose delivery failed, by task, as
// gates.Undelivered reads them through where.
func Undelivered(ctx context.Context, drafts Lister, where func(context.Context, gates.Query) ([]gate.Gate, error)) (map[task.ID][]gate.Gate, error) {
	ds, err := drafts.List(ctx, Filter{States: []draft.State{draft.Failed}})
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(ds))
	for i, d := range ds {
		ids[i] = int64(d.ID)
	}
	return gates.Undelivered(ctx, where, ids)
}
