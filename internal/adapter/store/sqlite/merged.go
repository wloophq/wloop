package sqlite

import (
	"context"

	"github.com/wloophq/wloop/internal/domain/task"
)

// MergedInto are the tasks merged into task id, transitively, in merge order: the old ids of the aliases
// that lead to id, hop by hop, that no stored task holds.
func (db *DB) MergedInto(ctx context.Context, id task.ID) ([]task.ID, error) {
	panic("not implemented: T10")
}
