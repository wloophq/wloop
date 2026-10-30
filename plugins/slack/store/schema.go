package store

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/wloophq/wloop/pkg/plugin/store"
)

//go:embed schema.sql
var schema string

var migrations = []store.Migration{
	{Version: 1, Up: func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, schema)
		return err
	}},
	{Version: 2, Up: func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS ledger_migrations (name TEXT PRIMARY KEY, at TEXT NOT NULL) WITHOUT ROWID`)
		return err
	}},
}

var tableRE = regexp.MustCompile(`CREATE TABLE (\w+) \(`)

// Tables are the store's tables, in schema order.
func Tables() []string {
	var out []string
	for _, m := range tableRE.FindAllStringSubmatch(schema, -1) {
		out = append(out, m[1])
	}
	return out
}

// briefsTable is what the host's export holds of the briefs its Slack units
// use, which the extraction reads as a unit's brief so far.
const briefsTable = "extraction_briefs"

// importTables copies every table of the export the store has, the columns
// both share, then the briefs the export holds.
func importTables(ctx context.Context, tx *sql.Tx) error {
	have, err := importedTables(ctx, tx)
	if err != nil {
		return err
	}
	for _, t := range Tables() {
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
	if !slices.Contains(have, briefsTable) {
		return nil
	}
	_, err = tx.ExecContext(ctx, fmt.Sprintf(`INSERT INTO main.extraction_cursors (provider, unit_key, brief, at)
		SELECT provider, unit_key, brief, '' FROM %s.%s WHERE true
		ON CONFLICT (provider, unit_key) DO UPDATE SET brief = excluded.brief`, store.ImportSchema, briefsTable))
	if err != nil {
		return fmt.Errorf("import %s: %w", briefsTable, err)
	}
	return nil
}

func importedTables(ctx context.Context, tx *sql.Tx) ([]string, error) {
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`SELECT name FROM %s.sqlite_master WHERE type = 'table'`, store.ImportSchema))
	if err != nil {
		return nil, err
	}
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

func columns(ctx context.Context, tx *sql.Tx, schema, table string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`SELECT name FROM pragma_table_info('%s', '%s')`, table, schema))
	if err != nil {
		return nil, err
	}
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
