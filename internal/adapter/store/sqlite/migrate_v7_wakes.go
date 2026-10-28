package sqlite

import (
	"context"
	"database/sql"
)

func v7Wakes(_ context.Context, _ *sql.Tx) error { return nil }
