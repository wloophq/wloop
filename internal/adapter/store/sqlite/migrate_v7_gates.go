package sqlite

import (
	"context"
	"database/sql"
)

func v7Gates(_ context.Context, _ *sql.Tx) error { return nil }
