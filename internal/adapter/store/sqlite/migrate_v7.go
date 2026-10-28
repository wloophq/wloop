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
		{"rounds", v7Rounds},
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

// v6States are the states version 6 stored and their categories, as the bundled profile of version 7
// declares them: the store cannot read the profile, so the migration carries them as history.
var v6States = []struct{ state, category string }{
	{"inbox", "inbox"}, {"next", "ready"}, {"waiting", "waiting"}, {"scheduled", "deferred"}, {"someday", "deferred"},
	{"reference", "done"}, {"done", "done"}, {"trash", "dropped"},
}
