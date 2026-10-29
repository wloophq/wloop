package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/gates/gatestest"
	"github.com/wloophq/wloop/internal/domain/task"
)

func TestGateStoreContract(t *testing.T) {
	t.Parallel()
	gatestest.StoreContract(t, func(t *testing.T) (gates.Store, gates.Reader, gates.Sent) {
		db, _ := openTemp(t)
		applyTask(t, db, "t-01")
		s := db.Gates(func() time.Time { return gatestest.Epoch })
		return s, s, s
	})
}

func applyTask(t *testing.T, db *DB, id task.ID) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.Write(ctx, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO tasks (id, state, origin, created, updated, rev) VALUES (?, 'inbox', 'user', '', '', 1)`, string(id))
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
