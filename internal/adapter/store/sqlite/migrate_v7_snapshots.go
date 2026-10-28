package sqlite

import (
	"context"
	"database/sql"
)

func v7Snapshots(_ context.Context, _ *sql.Tx) error { return nil }
