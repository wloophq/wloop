package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

// v7 takes a store from entries to tasks. Its steps run in this order, each in its own file, so a later step
// reads what the earlier ones wrote.
func v7(ctx context.Context, tx *sql.Tx) error {
	for _, s := range []struct {
		name string
		up   func(context.Context, *sql.Tx) error
	}{
		{"rename", v7Rename},
		{"snapshots", v7Snapshots},
		{"props", v7Props},
		{"gates", v7Gates},
		{"briefs", v7Briefs},
		{"kinds", v7Kinds},
		{"wakes", v7Wakes},
		{"pr tasks", v7PRTasks},
		{"dossiers", v7Dossiers},
		{"plugin stores", v7PluginStores},
	} {
		if err := s.up(ctx, tx); err != nil {
			return fmt.Errorf("the %s step: %w", s.name, err)
		}
	}
	return nil
}
