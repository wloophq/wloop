package sqlite

import (
	"context"
	"database/sql"
)

func v7Props(_ context.Context, _ *sql.Tx) error { return nil }
