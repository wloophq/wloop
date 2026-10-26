package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

// v4 keys briefs and tags by unit, makes brief_runs provider-neutral, keeps
// a sent draft's closure and when its effects were applied, records each
// turn's role and drops items.waiting_on, which nothing reads. It declares
// no compatible version: a v3 release refuses the store. Its data steps run
// between the additions and the drops, so each reads the columns and tables
// v4 drops.
func v4(ctx context.Context, tx *sql.Tx) error {
	if err := execAll(v4Add...)(ctx, tx); err != nil {
		return err
	}
	for _, step := range []struct {
		name string
		run  func(context.Context, *sql.Tx) error
	}{
		{"briefs", v4Briefs},
		{"closure", v4Closure},
		{"effects", v4Effects},
		{"roles", v4Roles},
		{"adopt", v4Adopt},
	} {
		if err := step.run(ctx, tx); err != nil {
			return fmt.Errorf("v4 %s: %w", step.name, err)
		}
	}
	return execAll(v4Drop...)(ctx, tx)
}

// v4Add are the additions of v4, in the order a new store lists them.
var v4Add = []string{
	`CREATE TABLE unit_briefs (
  provider TEXT NOT NULL, unit_key TEXT NOT NULL,
  failures INTEGER NOT NULL DEFAULT 0, attempt_of TEXT NOT NULL DEFAULT '',
  run INTEGER NOT NULL DEFAULT 0, last_run INTEGER NOT NULL DEFAULT 0,
  wanted_at TEXT NOT NULL DEFAULT '', retry_at TEXT NOT NULL DEFAULT '',
  gave_up_at TEXT NOT NULL DEFAULT '', gave_up_reason TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (provider, unit_key)
)`,
	`CREATE TABLE unit_tags (
  provider TEXT NOT NULL, unit_key TEXT NOT NULL,
  key TEXT NOT NULL, value TEXT NOT NULL, run INTEGER NOT NULL,
  PRIMARY KEY (provider, unit_key, key, value)
)`,
	`CREATE INDEX unit_tags_kv ON unit_tags(key, value)`,
	`ALTER TABLE brief_runs RENAME COLUMN diff_bytes TO input_bytes`,
	`ALTER TABLE brief_runs RENAME COLUMN diff_cut TO input_cut`,
	`ALTER TABLE brief_runs ADD COLUMN provider TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE brief_runs ADD COLUMN unit_key TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE brief_runs ADD COLUMN revision TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE brief_runs ADD COLUMN input TEXT NOT NULL DEFAULT '{}'`,
	`ALTER TABLE brief_runs ADD COLUMN intent TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE turns ADD COLUMN role TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE drafts ADD COLUMN effects_applied_at TEXT NOT NULL DEFAULT ''`,
	`CREATE INDEX drafts_workflow ON drafts (workflow, provider, unit_key, id)`,
	`ALTER TABLE extraction_cursors ADD COLUMN closure_marked INTEGER NOT NULL DEFAULT 0`,
}

// v4Drop are the drops of v4, after its data steps.
var v4Drop = []string{
	`DROP TABLE pr_briefs`,
	`DROP TABLE pr_tags`,
	`ALTER TABLE brief_runs DROP COLUMN owner`,
	`ALTER TABLE brief_runs DROP COLUMN repo`,
	`ALTER TABLE brief_runs DROP COLUMN number`,
	`ALTER TABLE brief_runs DROP COLUMN head_oid`,
	`ALTER TABLE brief_runs DROP COLUMN body_edited_at`,
	`ALTER TABLE brief_runs DROP COLUMN changed_files`,
	`ALTER TABLE brief_runs DROP COLUMN files`,
	`ALTER TABLE extraction_cursors DROP COLUMN brief`,
	`ALTER TABLE items DROP COLUMN waiting_on`,
}
