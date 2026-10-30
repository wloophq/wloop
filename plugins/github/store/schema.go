package store

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"slices"
	"strings"

	"github.com/wloophq/wloop/pkg/plugin/store"
)

//go:embed schema.sql
var schema string

var migrations = []store.Migration{{Version: 1, Up: func(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, schema)
	return err
}}}

// copiedTables are the tables of the export the import copies whole, the
// columns both share.
var copiedTables = []string{"pull_requests", "pr_seen", "pr_details", "fetches", "meta"}

// importTables copies the export's GitHub tables, then the GitHub rows of
// its overrides that set a column the plugin keeps; the host keeps the
// rest of each override.
func importTables(ctx context.Context, tx *sql.Tx) error {
	have, err := importedTables(ctx, tx)
	if err != nil {
		return err
	}
	for _, t := range copiedTables {
		if !slices.Contains(have, t) {
			continue
		}
		cols, err := sharedColumns(ctx, tx, t)
		if err != nil {
			return err
		}
		list := strings.Join(cols, ", ")
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`INSERT OR REPLACE INTO main.%s (%s) SELECT %s FROM %s.%s`, t, list, list, store.ImportSchema, t)); err != nil {
			return fmt.Errorf("import %s: %w", t, err)
		}
	}
	if !slices.Contains(have, "overrides") {
		return nil
	}
	_, err = tx.ExecContext(ctx, fmt.Sprintf(`INSERT OR REPLACE INTO main.overrides (unit_key, taken_over, tracked)
		SELECT unit_key, taken_over, tracked FROM %s.overrides WHERE provider = 'github' AND (taken_over OR tracked)`, store.ImportSchema))
	if err != nil {
		return fmt.Errorf("import overrides: %w", err)
	}
	return nil
}

func importedTables(ctx context.Context, tx *sql.Tx) ([]string, error) {
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`SELECT name FROM %s.sqlite_master WHERE type = 'table'`, store.ImportSchema))
	if err != nil {
		return nil, err
	}
	return scanStrings(rows)
}

func columns(ctx context.Context, tx *sql.Tx, schema, table string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT name FROM pragma_table_info(?, ?)`, table, schema)
	if err != nil {
		return nil, err
	}
	return scanStrings(rows)
}

func scanStrings(rows *sql.Rows) ([]string, error) {
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func sharedColumns(ctx context.Context, tx *sql.Tx, table string) ([]string, error) {
	mine, err := columns(ctx, tx, "main", table)
	if err != nil {
		return nil, err
	}
	theirs, err := columns(ctx, tx, store.ImportSchema, table)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(mine, func(c string) bool { return !slices.Contains(theirs, c) }), nil
}
