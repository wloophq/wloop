package sqlite

import (
	"context"
	"database/sql"
)

func v7Kinds(_ context.Context, _ *sql.Tx) error { return nil }
