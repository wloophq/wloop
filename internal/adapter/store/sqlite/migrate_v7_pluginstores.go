package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// v7Export is a table the store hands a plugin once: the rows where selects, all when empty. A kept table
// stays in the core, which still reads it. A table the core has not is ddl, filled by the SELECT from.
type v7Export struct {
	table, where string
	keep         bool
	ddl, from    string
}

// source is the SELECT of the rows e exports.
func (e v7Export) source() string {
	if e.from != "" {
		return e.from
	}
	if e.where == "" {
		return `SELECT * FROM main.` + e.table
	}
	return `SELECT * FROM main.` + e.table + ` WHERE ` + e.where
}

// v7PluginExport is what one plugin's import reads, written to dir/import.db under the store's directory, which
// the export attaches as schema.
type v7PluginExport struct {
	dir, schema string
	tables      []v7Export
}

// v7GitHubMeta are GitHub's meta keys; the host keeps its copy of the viewer, the first.
var v7GitHubMeta = []string{"viewer:github", "github_budget", "fast_sweep_at", "slow_sweep_at", "scope_resolved", "explore_recent"}

// v7Exports are the tables each plugin's import reads. fetches is the log of the host's workspace fetches, which
// GitHub's store also keeps.
var v7Exports = []v7PluginExport{
	{
		dir: "plugins/github", schema: "export_github",
		tables: []v7Export{
			{table: "pull_requests"}, {table: "pr_seen"}, {table: "pr_details"},
			{table: "fetches", keep: true},
			{table: "overrides", where: `provider = 'github'`, keep: true},
			{table: "meta", where: `key IN ('` + strings.Join(v7GitHubMeta, `', '`) + `')`, keep: true},
		},
	},
	{
		dir: "plugins/slack", schema: "export_slack",
		tables: []v7Export{
			{table: "segment_map"},
			{table: "extraction_cursors"}, {table: "extraction_hints"}, {table: "extraction_days"}, {table: "extraction_runs"},
			{table: "slack_conversations"}, {table: "slack_objects"}, {table: "slack_messages"}, {table: "slack_reactions"},
			{table: "slack_users"}, {table: "slack_changes"}, {table: "slack_cursors"},
			{table: "extraction_briefs", keep: true,
				ddl: `CREATE TABLE extraction_briefs (provider TEXT NOT NULL, unit_key TEXT NOT NULL, brief TEXT NOT NULL, PRIMARY KEY (provider, unit_key))`,
				from: `SELECT b.provider, b.unit_key, r.brief FROM main.unit_briefs b JOIN main.brief_runs r ON r.id = b.run
					WHERE b.provider = 'slack' AND b.run != 0 AND json_valid(r.brief) AND json_type(r.brief) = 'object'`},
		},
	},
}

// v7ExportPlugins writes each plugin's import.db before version 7's transaction, in a transaction of its own
// that writes that file alone, checks its row counts against the core's, syncs it and only then gives it its
// name: an import.db is a complete export, which a migration run again keeps. The store's own commit cannot
// cover it, since a WAL store commits apart from the files attached to it.
func v7ExportPlugins(ctx context.Context, conn *sql.Conn, storeDir string) error {
	for _, p := range v7Exports {
		path := filepath.Join(storeDir, p.dir, "import.db")
		done, err := v7ExportComplete(ctx, conn, p, path)
		if err != nil {
			return err
		}
		if done {
			continue
		}
		if err := v7WriteExport(ctx, conn, p, path); err != nil {
			return fmt.Errorf("exporting to %s: %w", path, err)
		}
	}
	return nil
}

// v7ExportComplete says path is p's export with every row the core holds; a file that does not read is not.
func v7ExportComplete(ctx context.Context, conn *sql.Conn, p v7PluginExport, path string) (bool, error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if _, err := conn.ExecContext(ctx, `ATTACH DATABASE ? AS `+p.schema, path); err != nil {
		return false, nil
	}
	defer func() { _, _ = conn.ExecContext(context.WithoutCancel(ctx), `DETACH DATABASE `+p.schema) }()
	return v7ExportCounts(ctx, conn, p) == nil, nil
}

// v7ExportCounts fails when an exported table of p's attached export lacks a row of its source.
func v7ExportCounts(ctx context.Context, conn *sql.Conn, p v7PluginExport) error {
	for _, e := range p.tables {
		var want, got int
		if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM (`+e.source()+`)`).Scan(&want); err != nil {
			return err
		}
		if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM `+p.schema+`.`+e.table).Scan(&got); err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("%s holds %d rows of the core's %d", e.table, got, want)
		}
	}
	return nil
}

func v7WriteExport(ctx context.Context, conn *sql.Conn, p v7PluginExport, path string) (err error) {
	tmp := path + ".tmp"
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	for _, f := range []string{tmp, tmp + "-journal"} {
		if err := os.Remove(f); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, storeMode)
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, `ATTACH DATABASE ? AS `+p.schema, tmp); err != nil {
		return err
	}
	attached := true
	defer func() {
		if attached {
			_, _ = conn.ExecContext(context.WithoutCancel(ctx), `DETACH DATABASE `+p.schema)
		}
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	for _, q := range []string{`PRAGMA ` + p.schema + `.journal_mode = DELETE`, `PRAGMA ` + p.schema + `.synchronous = FULL`} {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	if err := migrationTx(ctx, conn, func(tx *sql.Tx) error {
		tables := make([]string, 0, len(p.tables))
		for _, e := range p.tables {
			if err := v7Export1(ctx, tx, p.schema, e); err != nil {
				return fmt.Errorf("%s: %w", e.table, err)
			}
			tables = append(tables, e.table)
		}
		// The export runs before v7Kinds rewrites the core's times, and a plugin reads one layout.
		return v7FixTimes(ctx, tx, p.schema, tables)
	}); err != nil {
		return err
	}
	if err := v7ExportCounts(ctx, conn, p); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, `DETACH DATABASE `+p.schema); err != nil {
		return err
	}
	attached = false
	if err := syncFile(tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return syncFile(filepath.Dir(path))
}

func syncFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}

func v7Export1(ctx context.Context, tx *sql.Tx, schema string, e v7Export) error {
	ddl := e.ddl
	if ddl == "" {
		if err := tx.QueryRowContext(ctx, `SELECT sql FROM main.sqlite_schema WHERE type = 'table' AND name = ?`, e.table).Scan(&ddl); err != nil {
			return err
		}
	}
	head := "CREATE TABLE " + e.table + " "
	if !strings.HasPrefix(ddl, head) {
		return fmt.Errorf("its definition starts %.40q", ddl)
	}
	for _, q := range []string{
		"CREATE TABLE " + schema + "." + e.table + " " + strings.TrimPrefix(ddl, head),
		`INSERT INTO ` + schema + `.` + e.table + ` ` + e.source(),
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

// v7BeforeDrop, when set, runs before the core drops the exported tables of the store in a directory.
var v7BeforeDrop func(storeDir string) error

// v7PluginStores drops from the core the tables v7ExportPlugins exported, and GitHub's meta rows but the
// viewer. It runs last, so every earlier step reads them here.
func v7PluginStores(ctx context.Context, tx *sql.Tx) error {
	if v7BeforeDrop != nil {
		var file string
		if err := tx.QueryRowContext(ctx, `SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(&file); err != nil {
			return err
		}
		if err := v7BeforeDrop(filepath.Dir(file)); err != nil {
			return err
		}
	}
	for _, p := range v7Exports {
		for _, e := range p.tables {
			if e.keep {
				continue
			}
			if _, err := tx.ExecContext(ctx, `DROP TABLE main.`+e.table); err != nil {
				return err
			}
		}
	}
	for _, key := range v7GitHubMeta[1:] {
		if _, err := tx.ExecContext(ctx, `DELETE FROM main.meta WHERE key = ?`, key); err != nil {
			return err
		}
	}
	return nil
}
