package sqlite

import (
	"context"
	"database/sql"
)

func v7PluginStores(_ context.Context, _ *sql.Tx) error { return nil }
