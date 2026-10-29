package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"path"
	"strings"
)

// v7Export is a table the store hands a plugin once: the rows where selects, all when empty. A kept table
// stays in the core, which still reads it.
type v7Export struct {
	table, where string
	keep         bool
}

// v7PluginExport is what one plugin's import reads, written to dir/import.db under the store's directory, which
// the migration attaches as schema; then runs after the tables.
type v7PluginExport struct {
	dir, schema string
	tables      []v7Export
	then        string
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
		then: `DELETE FROM main.meta WHERE key IN ('` + strings.Join(v7GitHubMeta[1:], `', '`) + `')`,
	},
	{
		dir: "plugins/slack", schema: "export_slack",
		tables: []v7Export{
			{table: "segment_map"},
			{table: "extraction_cursors"}, {table: "extraction_hints"}, {table: "extraction_days"}, {table: "extraction_runs"},
			{table: "slack_conversations"}, {table: "slack_objects"}, {table: "slack_messages"}, {table: "slack_reactions"},
			{table: "slack_users"}, {table: "slack_changes"}, {table: "slack_cursors"},
		},
		// The brief each Slack unit uses, which the Slack import reads as its extraction's brief so far.
		then: `DROP TABLE IF EXISTS export_slack.extraction_briefs;
CREATE TABLE export_slack.extraction_briefs (
	provider TEXT NOT NULL, unit_key TEXT NOT NULL, brief TEXT NOT NULL, PRIMARY KEY (provider, unit_key));
INSERT INTO export_slack.extraction_briefs (provider, unit_key, brief)
	SELECT b.provider, b.unit_key, r.brief FROM unit_briefs b JOIN brief_runs r ON r.id = b.run
	WHERE b.provider = 'slack' AND b.run != 0 AND json_valid(r.brief) AND json_type(r.brief) = 'object'`,
	},
}

// v7ExportFiles are the import.db files of the plugins by the schema each is attached as.
func v7ExportFiles() map[string]string {
	files := map[string]string{}
	for _, p := range v7Exports {
		files[p.schema] = path.Join(p.dir, "import.db")
	}
	return files
}

// v7PluginStores copies each plugin's tables, with their shape, into the plugin's import.db, which it imports
// on its first start, then drops them from the core. It runs last, so every earlier step reads them here.
func v7PluginStores(ctx context.Context, tx *sql.Tx) error {
	for _, p := range v7Exports {
		for _, e := range p.tables {
			if err := v7Export1(ctx, tx, p.schema, e); err != nil {
				return fmt.Errorf("exporting %s to %s: %w", e.table, p.dir, err)
			}
		}
		if _, err := tx.ExecContext(ctx, p.then); err != nil {
			return fmt.Errorf("exporting to %s: %w", p.dir, err)
		}
	}
	return nil
}

func v7Export1(ctx context.Context, tx *sql.Tx, schema string, e v7Export) error {
	var ddl string
	if err := tx.QueryRowContext(ctx, `SELECT sql FROM main.sqlite_schema WHERE type = 'table' AND name = ?`, e.table).Scan(&ddl); err != nil {
		return err
	}
	head := "CREATE TABLE " + e.table + " "
	if !strings.HasPrefix(ddl, head) {
		return fmt.Errorf("its definition starts %.40q", ddl)
	}
	where := ""
	if e.where != "" {
		where = " WHERE " + e.where
	}
	for _, q := range []string{
		`DROP TABLE IF EXISTS ` + schema + `.` + e.table,
		"CREATE TABLE " + schema + "." + e.table + " " + strings.TrimPrefix(ddl, head),
		`INSERT INTO ` + schema + `.` + e.table + ` SELECT * FROM main.` + e.table + where,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	if e.keep {
		return nil
	}
	_, err := tx.ExecContext(ctx, `DROP TABLE main.`+e.table)
	return err
}
